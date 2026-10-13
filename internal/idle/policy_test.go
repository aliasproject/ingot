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

func withProcs(n int) func(*Sample) {
	return func(s *Sample) { quiet(s); s.Procs = n }
}

func base() Sample { return Sample{At: t0, CPUNanos: 1e9, NetBytes: 1000, Procs: 1} }

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
		"cpu":       {quiet, busyCPU},
		"network":   {quiet, traffic},
		"processes": {quiet, withProcs(3), withProcs(3)},
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

func TestSingleExtraProcessIsIgnored(t *testing.T) {
	// A health check caught mid-run shows up as one extra process in one
	// sample. It must not keep the container awake.
	a := feed(NewTracker(DefaultThresholds), base(), quiet, withProcs(2), withProcs(1), quiet)
	if a.Active {
		t.Fatalf("one-sample process blip counted as activity: %v", a.Reasons)
	}
}

func TestProcessBaselineDropsToTheServerAlone(t *testing.T) {
	// First seen while `composer install` was running (3 processes); once it
	// finishes the baseline must fall to 1, so a later job counts again.
	b := base()
	b.Procs = 3
	a := feed(NewTracker(DefaultThresholds), b, withProcs(1), withProcs(1), withProcs(2), withProcs(2))
	if !a.Active {
		t.Fatal("a job started after the baseline dropped wasn't seen")
	}
}

func TestRestartStartsAFreshTimer(t *testing.T) {
	tr := NewTracker(DefaultThresholds)
	feed(tr, base(), quiet, quiet, quiet)
	a := tr.Observe("c1", "run2", Sample{At: t0.Add(5 * time.Minute), CPUNanos: 5, NetBytes: 5, Procs: 1})
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
