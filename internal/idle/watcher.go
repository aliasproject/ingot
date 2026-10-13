package idle

import (
	"context"
	"log"
	"strings"
	"time"
)

// BusyWarning starts the log line the watcher writes when a container has
// been busy for a long time with no traffic. `alios doctor` looks for it.
const BusyWarning = "busy-warning"

// Watcher stops project containers once they've been idle for their timeout.
type Watcher struct {
	Engine   Engine
	Tracker  *Tracker
	Interval time.Duration
	// Timeout is the default idle time before a stop; zero disables stopping
	// for projects without their own IdleLabel timeout.
	Timeout time.Duration
	// BusyWarnAfter is how long a container may stay busy without any
	// traffic before the watcher logs a BusyWarning about it.
	BusyWarnAfter time.Duration
	Log           *log.Logger

	warned map[string]bool
}

// Run samples every Interval until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		w.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tick samples every running project once and stops the ones that are idle.
func (w *Watcher) Tick(ctx context.Context) {
	if w.warned == nil {
		w.warned = make(map[string]bool)
	}

	containers, err := w.Engine.ListProjects(ctx)
	if err != nil {
		w.Log.Printf("listing projects: %v", err)
		return
	}

	seen := make(map[string]bool, len(containers))
	for _, listed := range containers {
		if !listed.Running {
			continue
		}
		timeout, ok := w.timeoutFor(listed)
		if !ok {
			continue
		}

		// Kept in the tracker even if this tick's reading fails, so a
		// transient API error doesn't restart the project's idle timer.
		seen[listed.ID] = true

		// The listing doesn't carry StartedAt or the main PID; Inspect does.
		// StartedAt is how the tracker tells a restarted container from the
		// same run.
		c, err := w.Engine.Inspect(ctx, listed.ID)
		if err != nil || !c.Running {
			continue
		}
		s, err := w.Engine.Sample(ctx, c)
		if err != nil {
			w.Log.Printf("sampling %s: %v", c.Project, err)
			continue
		}

		a := w.Tracker.Observe(c.ID, c.StartedAt, s)
		w.warnIfBusy(c, a)

		if a.Active || a.IdleFor < timeout {
			continue
		}
		if err := w.Engine.Stop(ctx, c.ID, 10*time.Second); err != nil {
			w.Log.Printf("stopping %s: %v", c.Project, err)
			continue
		}
		w.Log.Printf("stopped %s after %s idle (wakes on the next request)", c.Project, a.IdleFor.Round(time.Second))
	}

	w.Tracker.Forget(seen)
	for id := range w.warned {
		if !seen[id] {
			delete(w.warned, id)
		}
	}
}

// timeoutFor returns the idle timeout for c, or false when c should never be
// stopped: the project opted out, or there's no default and no override.
func (w *Watcher) timeoutFor(c Container) (time.Duration, bool) {
	setting := strings.TrimSpace(c.Idle)
	switch setting {
	case "":
		return w.Timeout, w.Timeout > 0
	case "off", "never":
		return 0, false
	}
	d, err := time.ParseDuration(setting)
	if err != nil || d <= 0 {
		w.Log.Printf("%s: ignoring invalid %s=%q, using the default", c.Project, IdleLabel, setting)
		return w.Timeout, w.Timeout > 0
	}
	return d, true
}

func (w *Watcher) warnIfBusy(c *Container, a Activity) {
	if w.BusyWarnAfter <= 0 || a.BusyWithoutTrafficFor < w.BusyWarnAfter {
		if a.BusyWithoutTrafficFor == 0 {
			delete(w.warned, c.ID)
		}
		return
	}
	if w.warned[c.ID] {
		return
	}
	w.warned[c.ID] = true
	w.Log.Printf("%s %s: busy for %s with no traffic (%s); check it's not stuck with 'alios logs' or 'docker top %s'",
		BusyWarning, c.Project, a.BusyWithoutTrafficFor.Round(time.Minute), strings.Join(a.Reasons, ", "), c.Name)
}

// DefaultTimeout is how long a project may sit idle before it's stopped when
// ALIOS_IDLE_TIMEOUT isn't set.
const DefaultTimeout = 30 * time.Minute

// ParseTimeout reads a timeout setting: a Go duration, or "off"/"0" to
// disable. An empty string means the default.
func ParseTimeout(value string, def time.Duration) (time.Duration, error) {
	switch strings.TrimSpace(value) {
	case "":
		return def, nil
	case "off", "never", "0":
		return 0, nil
	}
	return time.ParseDuration(strings.TrimSpace(value))
}
