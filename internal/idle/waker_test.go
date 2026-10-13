package idle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// shop is a project container that, if running, has been up for an hour.
func shop(running bool) Container {
	return Container{ID: "id-shop", Name: "ingot_php_shop", Project: "shop", Running: running,
		StartedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}
}

// readyWhenRunning makes the fake server accept connections once its
// container is running, like the real one shortly after start.
func readyWhenRunning(f *fakeEngine) func(context.Context, string) bool {
	return func(_ context.Context, name string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		c := f.byID(name)
		return c != nil && c.Running
	}
}

func newWaker(f *fakeEngine) *Waker {
	return &Waker{Engine: f, Ready: readyWhenRunning(f), WakeTimeout: time.Second, Log: discard}
}

func wakeRequest(method, target, project string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if project != "" {
		r.Header.Set(ProjectHeader, project)
	}
	return r
}

func TestWakeStartsContainerAndRedirectsToSameURL(t *testing.T) {
	f := newFakeEngine(shop(false))
	rw := httptest.NewRecorder()
	newWaker(f).ServeHTTP(rw, wakeRequest(http.MethodPost, "/cart/add?id=7", "shop"))

	if rw.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307 (keeps the POST)", rw.Code)
	}
	if loc := rw.Header().Get("Location"); loc != "/cart/add?id=7" {
		t.Fatalf("Location = %q, want the original path and query", loc)
	}
	if f.starts != 1 || !f.containers["ingot_php_shop"].Running {
		t.Fatal("container wasn't started")
	}
}

func TestRunningButUnreachableDoesNotRedirectLoop(t *testing.T) {
	// Caddy failed to reach a container that is up and listening (say the
	// app's own server crashed mid-request). Redirecting would loop forever.
	f := newFakeEngine(shop(true))
	rw := httptest.NewRecorder()
	newWaker(f).ServeHTTP(rw, wakeRequest(http.MethodGet, "/", "shop"))

	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rw.Code)
	}
	if f.starts != 0 {
		t.Fatal("started a container that was already running")
	}
}

func TestOnlyStartsContainersOfTheNamedProject(t *testing.T) {
	// ingot_php_shop exists but belongs to some other project label - never
	// start a container ingot didn't create for this project.
	other := shop(false)
	other.Project = "not-shop"
	cases := map[string]*fakeEngine{
		"missing container": newFakeEngine(),
		"label mismatch":    newFakeEngine(other),
	}
	for name, f := range cases {
		rw := httptest.NewRecorder()
		newWaker(f).ServeHTTP(rw, wakeRequest(http.MethodGet, "/", "shop"))
		if rw.Code != http.StatusNotFound || f.starts != 0 {
			t.Errorf("%s: status=%d starts=%d, want 404 and no start", name, rw.Code, f.starts)
		}
	}
}

func TestRejectsMissingOrInvalidProjectHeader(t *testing.T) {
	for _, project := range []string{"", "../etc", "shop/../x", "-rm"} {
		f := newFakeEngine(shop(false))
		rw := httptest.NewRecorder()
		newWaker(f).ServeHTTP(rw, wakeRequest(http.MethodGet, "/", project))
		if rw.Code != http.StatusBadRequest || f.starts != 0 {
			t.Errorf("project %q: status=%d starts=%d, want 400 and no start", project, rw.Code, f.starts)
		}
	}
}

func TestConcurrentRequestsShareOneStart(t *testing.T) {
	f := newFakeEngine(shop(false))
	w := newWaker(f)
	var readyCalls atomic.Int32
	release := make(chan struct{})
	w.Ready = func(ctx context.Context, name string) bool {
		// Hold the first wake open until every request has arrived.
		if readyCalls.Add(1) == 1 {
			<-release
		}
		return readyWhenRunning(f)(ctx, name)
	}

	var wg sync.WaitGroup
	codes := make([]int, 5)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rw := httptest.NewRecorder()
			w.ServeHTTP(rw, wakeRequest(http.MethodGet, "/asset.css", "shop"))
			codes[i] = rw.Code
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusTemporaryRedirect {
			t.Errorf("request %d: status %d", i, code)
		}
	}
	if f.starts != 1 {
		t.Fatalf("starts = %d, want 1 shared start", f.starts)
	}
}

func TestStartFailureAndTimeoutReturnAnErrorPage(t *testing.T) {
	f := newFakeEngine(shop(false))
	f.startErr = errors.New("port is already allocated")
	rw := httptest.NewRecorder()
	newWaker(f).ServeHTTP(rw, wakeRequest(http.MethodGet, "/", "shop"))
	if rw.Code != http.StatusGatewayTimeout {
		t.Fatalf("start failure: status = %d, want 504", rw.Code)
	}

	f = newFakeEngine(shop(false))
	w := newWaker(f)
	w.WakeTimeout = 200 * time.Millisecond
	w.Ready = func(context.Context, string) bool { return false }
	rw = httptest.NewRecorder()
	w.ServeHTTP(rw, wakeRequest(http.MethodGet, "/", "shop"))
	if rw.Code != http.StatusGatewayTimeout {
		t.Fatalf("never ready: status = %d, want 504", rw.Code)
	}
}

func TestHealthz(t *testing.T) {
	rw := httptest.NewRecorder()
	newWaker(newFakeEngine()).ServeHTTP(rw, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rw.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rw.Code)
	}
}

func TestRequestJustAfterAWakeIsRetriedNotFailed(t *testing.T) {
	// A page's asset request that Caddy failed while the container was down
	// reaches the waker after the page's own request already woke it.
	f := newFakeEngine(shop(false))
	w := newWaker(f)
	w.ServeHTTP(httptest.NewRecorder(), wakeRequest(http.MethodGet, "/", "shop"))

	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, wakeRequest(http.MethodGet, "/app.css", "shop"))
	if rw.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307 so the browser retries", rw.Code)
	}
	if f.starts != 1 {
		t.Fatalf("starts = %d, want 1", f.starts)
	}
}

func TestRequestJustAfterAnotherStartIsRetried(t *testing.T) {
	// Started a moment ago by `ingot start` or `ingot exec`, not by the
	// waker: a request Caddy failed while it was down is still retried.
	c := shop(true)
	c.StartedAt = time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)
	f := newFakeEngine(c)
	rw := httptest.NewRecorder()
	newWaker(f).ServeHTTP(rw, wakeRequest(http.MethodGet, "/", "shop"))
	if rw.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307", rw.Code)
	}
}

func TestRedirectNeverLeavesTheSite(t *testing.T) {
	for target, want := range map[string]string{
		"//evil.com/login":   "/evil.com/login",
		"/\\evil.com":        "/%5Cevil.com", // Go escapes the backslash; browsers keep it on-site
		"///x?y=1":           "/x?y=1",
		"/":                  "/",
		"/cart?next=//b.com": "/cart?next=//b.com",
	} {
		f := newFakeEngine(shop(false))
		r := wakeRequest(http.MethodGet, "/", "shop")
		r.RequestURI = target
		r.URL.Path, r.URL.RawQuery = target, ""
		if i := strings.IndexByte(target, '?'); i >= 0 {
			r.URL.Path, r.URL.RawQuery = target[:i], target[i+1:]
		}
		rw := httptest.NewRecorder()
		newWaker(f).ServeHTTP(rw, r)
		if loc := rw.Header().Get("Location"); loc != want {
			t.Errorf("%q redirected to %q, want %q", target, loc, want)
		}
	}
}

func TestWakeDuringAStopStartsItOnceTheStopFinishes(t *testing.T) {
	// The watcher (or `ingot stop`) is stopping the container when the
	// request arrives: it still counts as running, so the first Start is a
	// no-op. Once it has exited, the waker must start it again rather than
	// wait for a port that will never open.
	c := shop(true)
	f := newFakeEngine(c)
	w := newWaker(f)
	w.WakeTimeout = 5 * time.Second
	var ready atomic.Bool
	w.Ready = func(context.Context, string) bool { return ready.Load() }
	go func() {
		time.Sleep(300 * time.Millisecond)
		f.mu.Lock()
		f.containers["ingot_php_shop"].Running = false // the stop completes
		f.mu.Unlock()
		for {
			f.mu.Lock()
			up := f.containers["ingot_php_shop"].Running
			f.mu.Unlock()
			if up {
				ready.Store(true)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()

	rw := httptest.NewRecorder()
	w.ServeHTTP(rw, wakeRequest(http.MethodGet, "/", "shop"))
	if rw.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307 once restarted", rw.Code)
	}
	if f.starts < 2 {
		t.Fatalf("starts = %d, want a second start after the stop finished", f.starts)
	}
}
