package idle

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"
)

// ProjectHeader names the project a woken request is for. The vhost's
// alios_wake snippet sets it (see services.baseCaddyfile), so the waker never
// has to guess the project from a Host that might be a share's public name.
const ProjectHeader = "X-Alios-Project"

var validProject = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Waker answers the requests Caddy couldn't deliver because the project's
// container is stopped: it starts the container, waits until it accepts
// connections, then redirects the browser to the same URL to try again.
type Waker struct {
	Engine Engine
	// Ready reports whether the project's server accepts connections yet.
	// Defaults to dialing <container>:80 on the shared network.
	Ready       func(ctx context.Context, container string) bool
	WakeTimeout time.Duration
	Log         *log.Logger

	mu    sync.Mutex
	waits map[string]*wake
	woken map[string]time.Time // container ID -> when its last wake finished
}

// recentWake is how long after a wake a failed request is still assumed to be
// one that Caddy sent while the container was down, rather than a running
// project failing. Requests for a page's assets arrive in a burst, and the
// later ones can reach the waker after the first one already woke it.
const recentWake = 15 * time.Second

// wake is one start in progress. Concurrent requests for the same project
// (a page and its assets) share it instead of each starting the container.
type wake struct {
	done chan struct{}
	err  error
}

var errNotReady = errors.New("didn't accept connections in time")

func (w *Waker) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Header.Get(ProjectHeader) == "" {
		rw.WriteHeader(http.StatusNoContent)
		return
	}

	project := r.Header.Get(ProjectHeader)
	if !validProject.MatchString(project) {
		page(rw, http.StatusBadRequest, "Unknown project", "This request didn't name a project.")
		return
	}

	ctx := r.Context()
	c, err := w.Engine.Inspect(ctx, "alios_php_"+project)
	if errors.Is(err, ErrNotFound) || (err == nil && c.Project != project) {
		// Only ever start containers alios created for this project.
		page(rw, http.StatusNotFound, "No project here", fmt.Sprintf("There's no container for %s. Run 'alios start' in the project.", project))
		return
	}
	if err != nil {
		w.Log.Printf("inspecting %s: %v", project, err)
		page(rw, http.StatusBadGateway, "Couldn't check the project", err.Error())
		return
	}

	switch {
	case w.inProgress(c.ID) != nil || !c.Running || !w.ready(ctx, c.Name):
		if err := w.wakeOnce(c); err != nil {
			w.Log.Printf("waking %s: %v", project, err)
			page(rw, http.StatusGatewayTimeout, "Project didn't start",
				fmt.Sprintf("%s: %v. Check 'alios logs'.", project, err))
			return
		}
	case !w.wokeRecently(c.ID):
		// Caddy failed to reach a container that has been up and listening
		// for a while, so starting it again won't help - and redirecting
		// would loop.
		page(rw, http.StatusBadGateway, "Project isn't responding",
			fmt.Sprintf("%s is running but the request to it failed. Check 'alios logs'.", project))
		return
	}
	// Otherwise it's up because a wake just finished: retry as well.

	// 307 keeps the method and body, so a form POST that woke the project
	// is resubmitted rather than turned into a GET. The relative Location
	// keeps whatever scheme and host the browser used.
	rw.Header().Set("Cache-Control", "no-store")
	rw.Header().Set("Location", r.URL.RequestURI())
	rw.WriteHeader(http.StatusTemporaryRedirect)
}

func (w *Waker) inProgress(id string) *wake {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.waits[id]
}

func (w *Waker) wokeRecently(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	at, ok := w.woken[id]
	return ok && time.Since(at) < recentWake
}

func (w *Waker) wakeOnce(c *Container) error {
	w.mu.Lock()
	if w.waits == nil {
		w.waits = make(map[string]*wake)
	}
	if wk, ok := w.waits[c.ID]; ok {
		w.mu.Unlock()
		<-wk.done
		return wk.err
	}
	wk := &wake{done: make(chan struct{})}
	w.waits[c.ID] = wk
	w.mu.Unlock()

	// Detached from the request: if the browser gives up, the container
	// should still finish starting for the next attempt.
	ctx, cancel := context.WithTimeout(context.Background(), w.WakeTimeout)
	defer cancel()
	start := time.Now()
	wk.err = w.start(ctx, c)
	if wk.err == nil {
		w.Log.Printf("woke %s in %s", c.Project, time.Since(start).Round(time.Millisecond))
	}

	w.mu.Lock()
	delete(w.waits, c.ID)
	if wk.err == nil {
		if w.woken == nil {
			w.woken = make(map[string]time.Time)
		}
		w.woken[c.ID] = time.Now()
	}
	w.mu.Unlock()
	close(wk.done)
	return wk.err
}

func (w *Waker) start(ctx context.Context, c *Container) error {
	if err := w.Engine.Start(ctx, c.ID); err != nil {
		return err
	}
	for {
		if w.ready(ctx, c.Name) {
			return nil
		}
		select {
		case <-ctx.Done():
			return errNotReady
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (w *Waker) ready(ctx context.Context, container string) bool {
	if w.Ready != nil {
		return w.Ready(ctx, container)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(container, "80"))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func page(rw http.ResponseWriter, status int, title, detail string) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	rw.WriteHeader(status)
	fmt.Fprintf(rw, `<!doctype html><meta charset="utf-8"><title>%[1]s</title>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem">
<h1>%[1]s</h1><p>%[2]s</p></body>`, html.EscapeString(title), html.EscapeString(detail))
}
