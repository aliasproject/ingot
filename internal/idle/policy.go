// Package idle is the alios_idle service: it stops project containers that
// have gone quiet and starts them again when a request arrives for them.
//
// It runs as its own container (cmd/alios-idle) next to the other global
// services, and talks to the Docker Engine API over the mounted socket
// directly rather than through internal/docker.Client: that client shells out
// to the docker CLI, which the service's image doesn't ship.
package idle

import (
	"time"
)

// Sample is one reading of a container's raw counters. CPU and network are
// cumulative since the container started; Procs is the number of processes
// (not threads) running in it.
type Sample struct {
	At       time.Time
	CPUNanos uint64
	NetBytes uint64
	Procs    int
}

// Thresholds decide when a container counts as active between two samples.
type Thresholds struct {
	// CPUFraction is the share of one core, averaged over the interval,
	// above which the container is busy. Idle FrankenPHP containers sit near
	// 0.001; a running job is well above 0.01.
	CPUFraction float64
	// NetBytesPerMinute is the traffic (rx+tx) above which the container is
	// being used. Any page view or open dev-server connection clears it;
	// background ARP/DNS chatter doesn't.
	NetBytesPerMinute uint64
}

// DefaultThresholds are tuned against idle alios_php_* containers, which use
// about 0.1% of a core and a few hundred bytes of traffic an hour.
var DefaultThresholds = Thresholds{
	CPUFraction:       0.01,
	NetBytesPerMinute: 8 * 1024,
}

// Activity is what Tracker.Observe concludes from the newest sample.
type Activity struct {
	Active bool
	// Reasons lists what made the container active ("cpu", "network",
	// "processes", "new"), for logging.
	Reasons []string
	// IdleFor is how long the container has been quiet; zero when active.
	IdleFor time.Duration
	// BusyWithoutTrafficFor is how long the container has been busy (CPU or
	// extra processes) with no network traffic at all - a job running on its
	// own, or something stuck. Zero when that isn't the case.
	BusyWithoutTrafficFor time.Duration
}

type instance struct {
	startedAt   string
	last        Sample
	baseProcs   int
	extraBefore bool
	lastActive  time.Time
	busySince   time.Time
}

// Tracker remembers each container's previous sample and decides whether it
// is idle. It holds no state worth persisting: after a restart every
// container simply starts a fresh idle timer.
type Tracker struct {
	th        Thresholds
	instances map[string]*instance
}

// NewTracker returns a Tracker using the given thresholds.
func NewTracker(th Thresholds) *Tracker {
	return &Tracker{th: th, instances: make(map[string]*instance)}
}

// Observe records a sample for container id. startedAt identifies the
// container's current run: when it changes the container was restarted, and
// its counters and process baseline start over.
func (t *Tracker) Observe(id, startedAt string, s Sample) Activity {
	inst, ok := t.instances[id]
	if !ok || inst.startedAt != startedAt || s.CPUNanos < inst.last.CPUNanos || s.NetBytes < inst.last.NetBytes {
		t.instances[id] = &instance{startedAt: startedAt, last: s, baseProcs: s.Procs, lastActive: s.At}
		return Activity{Active: true, Reasons: []string{"new"}}
	}

	elapsed := s.At.Sub(inst.last.At)
	if elapsed <= 0 {
		return t.activity(inst, s.At, nil, false)
	}

	// The baseline is the fewest processes seen in this run: the server
	// itself. Anything above it is an exec'd command, a worker or a job.
	if s.Procs < inst.baseProcs {
		inst.baseProcs = s.Procs
	}
	extra := s.Procs > inst.baseProcs

	var reasons []string
	cpu := float64(s.CPUNanos-inst.last.CPUNanos) / float64(elapsed.Nanoseconds())
	if cpu > t.th.CPUFraction {
		reasons = append(reasons, "cpu")
	}
	netPerMin := float64(s.NetBytes-inst.last.NetBytes) / elapsed.Minutes()
	traffic := netPerMin > float64(t.th.NetBytesPerMinute)
	if traffic {
		reasons = append(reasons, "network")
	}
	// Extra processes count only when seen twice in a row, so a health check
	// that happens to be running at the moment of a sample doesn't reset the
	// timer. Real work that short shows up as CPU anyway.
	if extra && inst.extraBefore {
		reasons = append(reasons, "processes")
	}
	inst.extraBefore = extra
	inst.last = s

	return t.activity(inst, s.At, reasons, traffic)
}

func (t *Tracker) activity(inst *instance, now time.Time, reasons []string, traffic bool) Activity {
	if len(reasons) == 0 {
		inst.busySince = time.Time{}
		return Activity{IdleFor: now.Sub(inst.lastActive)}
	}

	inst.lastActive = now
	a := Activity{Active: true, Reasons: reasons}
	if traffic {
		inst.busySince = time.Time{}
	} else {
		if inst.busySince.IsZero() {
			inst.busySince = now
		}
		a.BusyWithoutTrafficFor = now.Sub(inst.busySince)
	}
	return a
}

// Forget drops containers that weren't in the latest listing, so a removed
// project doesn't linger in memory.
func (t *Tracker) Forget(keep map[string]bool) {
	for id := range t.instances {
		if !keep[id] {
			delete(t.instances, id)
		}
	}
}
