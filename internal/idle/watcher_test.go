package idle

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"
)

func project(name, idle string) Container {
	return Container{ID: "id-" + name, Name: "alios_php_" + name, Project: name, Idle: idle, Running: true, StartedAt: "run1"}
}

// quietSamples returns n samples 30s apart with background-noise deltas.
func quietSamples(n int) []Sample {
	s := base()
	out := []Sample{s}
	for i := 1; i < n; i++ {
		s.At = s.At.Add(30 * time.Second)
		quiet(&s)
		out = append(out, s)
	}
	return out
}

func newWatcher(f *fakeEngine, timeout time.Duration) *Watcher {
	return &Watcher{Engine: f, Tracker: NewTracker(DefaultThresholds), Timeout: timeout, Log: discard}
}

func ticks(w *Watcher, n int) {
	for i := 0; i < n; i++ {
		w.Tick(context.Background())
	}
}

func TestStopsAfterTimeoutOnly(t *testing.T) {
	f := newFakeEngine(project("shop", ""))
	f.samples["id-shop"] = quietSamples(10)
	w := newWatcher(f, 2*time.Minute)

	ticks(w, 4) // 1m30s of quiet after the first sample
	if len(f.stops) != 0 {
		t.Fatalf("stopped after %d ticks, before the 2m timeout", 4)
	}
	ticks(w, 1) // 2m
	if len(f.stops) != 1 || f.stops[0] != "shop" {
		t.Fatalf("stops = %v, want shop stopped at 2m idle", f.stops)
	}
}

func TestPerProjectSettings(t *testing.T) {
	f := newFakeEngine(project("off", "off"), project("short", "1m"), project("default", ""), project("bad", "soon"))
	for _, id := range []string{"id-off", "id-short", "id-default", "id-bad"} {
		f.samples[id] = quietSamples(10)
	}
	w := newWatcher(f, 0) // no default: only projects with their own timeout stop

	ticks(w, 6)
	if strings.Join(f.stops, ",") != "short" {
		t.Fatalf("stops = %v, want only the project with its own 1m timeout", f.stops)
	}
}

func TestStoppedContainersAreLeftAlone(t *testing.T) {
	c := project("shop", "")
	c.Running = false
	f := newFakeEngine(c)
	w := newWatcher(f, time.Minute)
	ticks(w, 5)
	if len(f.stops) != 0 {
		t.Fatal("stopped a container that wasn't running")
	}
}

func TestBusyWarningIsLoggedOncePerStreak(t *testing.T) {
	f := newFakeEngine(project("shop", ""))
	s := base()
	samples := []Sample{s}
	for i := 0; i < 6; i++ {
		s.At = s.At.Add(30 * time.Second)
		busyCPU(&s)
		samples = append(samples, s)
	}
	f.samples["id-shop"] = samples

	var buf bytes.Buffer
	w := newWatcher(f, time.Hour)
	w.Log = log.New(&buf, "", 0)
	w.BusyWarnAfter = time.Minute
	ticks(w, 7)

	if n := strings.Count(buf.String(), BusyWarning); n != 1 {
		t.Fatalf("logged %d busy warnings, want 1:\n%s", n, buf.String())
	}
	if len(f.stops) != 0 {
		t.Fatal("stopped a busy container")
	}
}

func TestParseTimeout(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 30 * time.Minute, "off": 0, "0": 0, "45m": 45 * time.Minute} {
		got, err := ParseTimeout(in, 30*time.Minute)
		if err != nil || got != want {
			t.Errorf("ParseTimeout(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseTimeout("soon", time.Minute); err == nil {
		t.Error("ParseTimeout accepted an invalid value")
	}
}
