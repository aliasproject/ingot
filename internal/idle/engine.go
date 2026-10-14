package idle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ProjectLabel marks a container as an ingot project; IdleLabel holds its
// per-project setting ("off", or a timeout such as "2h"). Both are written by
// project.Manager.createContainer.
const (
	ProjectLabel = "ingot.project.name"
	IdleLabel    = "ingot.project.idle"
)

// Container is the part of a container's state the service needs.
type Container struct {
	ID        string
	Name      string // without the leading slash
	Project   string // ProjectLabel value
	Idle      string // IdleLabel value
	Running   bool
	StartedAt string // RFC 3339; from Inspect only
	MainPID   int    // host PID of the main process; from Inspect only
}

// StartedWithin reports whether the container's current run began less than
// d ago.
func (c *Container) StartedWithin(d time.Duration, now time.Time) bool {
	at, err := time.Parse(time.RFC3339Nano, c.StartedAt)
	return err == nil && now.Sub(at) < d
}

// Engine is the Docker access the service needs. *SocketEngine implements it
// against the real daemon; tests use a fake.
type Engine interface {
	ListProjects(ctx context.Context) ([]Container, error)
	Inspect(ctx context.Context, name string) (*Container, error)
	Sample(ctx context.Context, c *Container) (Sample, error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string, timeout time.Duration) error
}

// ErrNotFound is returned by Inspect for a container that doesn't exist.
var ErrNotFound = fmt.Errorf("container not found")

// SocketEngine talks to the Docker Engine API over a unix socket.
type SocketEngine struct {
	http *http.Client
}

// NewSocketEngine returns an Engine using the daemon socket at path.
func NewSocketEngine(path string) *SocketEngine {
	return &SocketEngine{http: &http.Client{
		// Bounds every call, so a wedged container can't hang the watcher.
		// Generous enough for a stop, which waits up to 10s for the app.
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		},
	}}
}

func (e *SocketEngine) do(ctx context.Context, method, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, body)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s %s: decoding response: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// ListProjects returns every ingot project container, running or not.
func (e *SocketEngine) ListProjects(ctx context.Context) ([]Container, error) {
	filters := url.QueryEscape(fmt.Sprintf(`{"label":[%q]}`, ProjectLabel))
	var list []struct {
		ID     string `json:"Id"`
		Names  []string
		State  string
		Labels map[string]string
	}
	if _, err := e.do(ctx, http.MethodGet, "/containers/json?all=true&filters="+filters, &list); err != nil {
		return nil, err
	}

	out := make([]Container, 0, len(list))
	for _, c := range list {
		name := ""
		if len(c.Names) > 0 {
			name = trimSlash(c.Names[0])
		}
		out = append(out, Container{
			ID:      c.ID,
			Name:    name,
			Project: c.Labels[ProjectLabel],
			Idle:    c.Labels[IdleLabel],
			Running: c.State == "running",
		})
	}
	return out, nil
}

// Inspect returns one container by name or ID.
func (e *SocketEngine) Inspect(ctx context.Context, name string) (*Container, error) {
	var c struct {
		ID    string `json:"Id"`
		Name  string
		State struct {
			Running   bool
			StartedAt string
			Pid       int
		}
		Config struct {
			Labels map[string]string
		}
	}
	status, err := e.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(name)+"/json", &c)
	if status == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &Container{
		ID:        c.ID,
		Name:      trimSlash(c.Name),
		Project:   c.Config.Labels[ProjectLabel],
		Idle:      c.Config.Labels[IdleLabel],
		Running:   c.State.Running,
		StartedAt: c.State.StartedAt,
		MainPID:   c.State.Pid,
	}, nil
}

// Sample reads a running container's CPU and network counters and counts the
// processes exec'd into it. c must come from Inspect, for its MainPID.
func (e *SocketEngine) Sample(ctx context.Context, c *Container) (Sample, error) {
	id := c.ID
	var stats struct {
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
		} `json:"cpu_stats"`
		Networks map[string]struct {
			RxBytes uint64 `json:"rx_bytes"`
			TxBytes uint64 `json:"tx_bytes"`
		} `json:"networks"`
	}
	// one-shot skips the extra second the daemon otherwise spends collecting
	// a "previous" CPU reading; the tracker keeps its own.
	if _, err := e.do(ctx, http.MethodGet, "/containers/"+id+"/stats?stream=false&one-shot=true", &stats); err != nil {
		return Sample{}, err
	}

	// Default ps arguments (-ef) on purpose: they work with whatever ps the
	// daemon's host has, including Docker Desktop's VM.
	var top struct {
		Titles    []string
		Processes [][]string
	}
	if _, err := e.do(ctx, http.MethodGet, "/containers/"+id+"/top", &top); err != nil {
		return Sample{}, err
	}
	execProcs, err := countExecProcs(top.Titles, top.Processes, c.MainPID)
	if err != nil {
		return Sample{}, err
	}

	s := Sample{At: time.Now(), CPUNanos: stats.CPUStats.CPUUsage.TotalUsage, ExecProcs: execProcs}
	for _, n := range stats.Networks {
		s.NetBytes += n.RxBytes + n.TxBytes
	}
	return s, nil
}

// Start starts a container. Starting one that's already running is fine.
func (e *SocketEngine) Start(ctx context.Context, id string) error {
	_, err := e.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil)
	return err
}

// Stop stops a container, killing it if it hasn't exited after timeout.
func (e *SocketEngine) Stop(ctx context.Context, id string, timeout time.Duration) error {
	_, err := e.do(ctx, http.MethodPost, fmt.Sprintf("/containers/%s/stop?t=%d", id, int(timeout.Seconds())), nil)
	return err
}

// countExecProcs counts the processes in a `docker top` listing that don't
// descend from mainPID. Everything the container's own server starts (worker
// processes, a supervisor's queue workers) descends from it; `docker exec`
// sessions are started beside it by the runtime, so they don't.
func countExecProcs(titles []string, rows [][]string, mainPID int) (int, error) {
	pidCol, ppidCol := -1, -1
	for i, t := range titles {
		switch t {
		case "PID":
			pidCol = i
		case "PPID":
			ppidCol = i
		}
	}
	if pidCol < 0 || ppidCol < 0 {
		return 0, fmt.Errorf("docker top output has no PID/PPID columns: %v", titles)
	}

	parent := make(map[int]int, len(rows))
	for _, row := range rows {
		if len(row) <= pidCol || len(row) <= ppidCol {
			continue
		}
		pid, err1 := strconv.Atoi(row[pidCol])
		ppid, err2 := strconv.Atoi(row[ppidCol])
		if err1 == nil && err2 == nil {
			parent[pid] = ppid
		}
	}

	count := 0
	for pid := range parent {
		p := pid
		// Walk up while the parent is inside the container. Bounded by the
		// listing's size, so a malformed cycle can't loop forever.
		for i := 0; i <= len(parent) && p != mainPID; i++ {
			pp, ok := parent[p]
			if !ok {
				break
			}
			p = pp
		}
		if p != mainPID {
			count++
		}
	}
	return count, nil
}

func trimSlash(name string) string {
	if len(name) > 0 && name[0] == '/' {
		return name[1:]
	}
	return name
}
