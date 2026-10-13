package idle

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)

// feed observes a sequence of samples 30s apart, starting from base, and
// returns the activity for the last one.
func feed(tr *Tracker, base Sample, steps ...func(*Sample)) Activity {
	s := base
	a := tr.Observe("c1", "run1", s)
	for _, step := range steps {
		s.At = s.At.Add(30 * time.Second)
		step(&s)
		a = tr.Observe("c1", "run1", s)
	}
	return a
}

func quiet(s *Sample) { s.CPUNanos += 10_000_000; s.NetBytes += 100 } // 0.03% CPU, 200 B/min

func busyCPU(s *Sample) { s.CPUNanos += 15_000_000_000 } // half a core

func traffic(s *Sample) { s.NetBytes += 50_000 }

func withExec(n int) func(*Sample) {
	return func(s *Sample) { quiet(s); s.ExecProcs = n }
}

func base() Sample { return Sample{At: t0, CPUNanos: 1e9, NetBytes: 1000} }

func TestFirstSampleIsActive(t *testing.T) {
	a := NewTracker(DefaultThresholds).Observe("c1", "run1", base())
	if !a.Active {
		t.Fatal("a container seen for the first time must get a fresh idle timer, not count as idle")
	}
}

func TestQuietContainerAccumulatesIdleTime(t *testing.T) {
	a := feed(NewTracker(DefaultThresholds), base(), quiet, quiet, quiet, quiet)
	if a.Active {
		t.Fatalf("background noise counted as activity: %v", a.Reasons)
	}
	if a.IdleFor != 2*time.Minute {
		t.Fatalf("IdleFor = %v, want 2m (four quiet 30s intervals)", a.IdleFor)
	}
}

func TestEachSignalCountsAsActivity(t *testing.T) {
	cases := map[string][]func(*Sample){
		"cpu":     {quiet, busyCPU},
		"network": {quiet, traffic},
		"exec":    {quiet, withExec(1), withExec(1)},
	}
	for want, steps := range cases {
		a := feed(NewTracker(DefaultThresholds), base(), steps...)
		if !a.Active || len(a.Reasons) == 0 || a.Reasons[0] != want {
			t.Errorf("%s: got active=%v reasons=%v", want, a.Active, a.Reasons)
		}
	}
}

func TestActivityResetsIdleTimer(t *testing.T) {
	a := feed(NewTracker(DefaultThresholds), base(), quiet, quiet, traffic, quiet)
	if a.IdleFor != 30*time.Second {
		t.Fatalf("IdleFor = %v, want 30s since the last request", a.IdleFor)
	}
}

func TestSingleExecBlipIsIgnored(t *testing.T) {
	// A health check caught mid-run shows up as one exec'd process in one
	// sample. It must not keep the container awake.
	a := feed(NewTracker(DefaultThresholds), base(), quiet, withExec(1), withExec(0), quiet)
	if a.Active {
		t.Fatalf("one-sample exec blip counted as activity: %v", a.Reasons)
	}
}

func TestShellOpenFromTheStartKeepsItAwake(t *testing.T) {
	// `ingot shell` on a sleeping project starts the container and execs a
	// shell before the watcher's first sample. An idle shell uses no CPU or
	// container network; it must still count for as long as it's open.
	b := base()
	b.ExecProcs = 1
	steps := []func(*Sample){}
	for i := 0; i < 120; i++ { // an hour
		steps = append(steps, withExec(1))
	}
	if a := feed(NewTracker(DefaultThresholds), b, steps...); !a.Active {
		t.Fatalf("open shell not counted: idle for %v", a.IdleFor)
	}
}

func TestRestartStartsAFreshTimer(t *testing.T) {
	tr := NewTracker(DefaultThresholds)
	feed(tr, base(), quiet, quiet, quiet)
	a := tr.Observe("c1", "run2", Sample{At: t0.Add(5 * time.Minute), CPUNanos: 5, NetBytes: 5})
	if !a.Active {
		t.Fatal("a restarted container must not inherit the idle time of its previous run")
	}
}

func TestBusyWithoutTrafficIsReported(t *testing.T) {
	a := feed(NewTracker(DefaultThresholds), base(), busyCPU, busyCPU, busyCPU)
	if a.BusyWithoutTrafficFor != time.Minute {
		t.Fatalf("BusyWithoutTrafficFor = %v, want 1m", a.BusyWithoutTrafficFor)
	}
	a = feed(NewTracker(DefaultThresholds), base(), busyCPU, func(s *Sample) { busyCPU(s); traffic(s) })
	if a.BusyWithoutTrafficFor != 0 {
		t.Fatal("work driven by requests isn't 'busy without traffic'")
	}
}

func TestForgetDropsRemovedContainers(t *testing.T) {
	tr := NewTracker(DefaultThresholds)
	tr.Observe("c1", "run1", base())
	tr.Observe("c2", "run1", base())
	tr.Forget(map[string]bool{"c2": true})
	if _, ok := tr.instances["c1"]; ok {
		t.Fatal("c1 wasn't forgotten")
	}
	if _, ok := tr.instances["c2"]; !ok {
		t.Fatal("c2 was forgotten")
	}
}

func TestIdleShellIsNotBusyWithoutTraffic(t *testing.T) {
	// An open `ingot shell` keeps the project awake, but it isn't a stuck
	// job: it must not trigger busy warnings.
	steps := []func(*Sample){}
	for i := 0; i < 10; i++ {
		steps = append(steps, withExec(1))
	}
	if a := feed(NewTracker(DefaultThresholds), base(), steps...); !a.Active || a.BusyWithoutTrafficFor != 0 {
		t.Fatalf("active=%v busy=%v, want active and not busy", a.Active, a.BusyWithoutTrafficFor)
	}
}
