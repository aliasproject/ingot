package idle

import (
	"context"
	"io"
	"log"
	"sync"
	"time"
)

// fakeEngine is an in-memory Docker for the waker and watcher tests.
type fakeEngine struct {
	mu         sync.Mutex
	containers map[string]*Container // by name
	samples    map[string][]Sample   // by ID, consumed in order
	starts     int
	stops      []string
	startErr   error
}

func newFakeEngine(cs ...Container) *fakeEngine {
	f := &fakeEngine{containers: map[string]*Container{}, samples: map[string][]Sample{}}
	for i := range cs {
		c := cs[i]
		f.containers[c.Name] = &c
	}
	return f
}

func (f *fakeEngine) byID(id string) *Container {
	for _, c := range f.containers {
		if c.ID == id || c.Name == id {
			return c
		}
	}
	return nil
}

func (f *fakeEngine) ListProjects(context.Context) ([]Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Container
	for _, c := range f.containers {
		if c.Project != "" {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (*Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byID(name)
	if c == nil {
		return nil, ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (f *fakeEngine) Sample(_ context.Context, id string) (Sample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	queue := f.samples[id]
	s := queue[0]
	if len(queue) > 1 {
		f.samples[id] = queue[1:]
	}
	return s, nil
}

func (f *fakeEngine) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	f.byID(id).Running = true
	return nil
}

func (f *fakeEngine) Stop(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.byID(id)
	c.Running = false
	f.stops = append(f.stops, c.Project)
	return nil
}

var discard = log.New(io.Discard, "", 0)
