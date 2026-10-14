// Command ingot-idle is the ingot_idle global service: it stops project
// containers that have gone idle and wakes them on the next request. It runs
// in its own container (see Dockerfile.idle); the CLI starts it with the other
// global services and configures it through environment variables.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aliasproject/ingot/internal/idle"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	listen := envOr("INGOT_IDLE_LISTEN", ":8080")

	// The image has no shell or wget for a Docker healthcheck, so the binary
	// checks itself.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		resp, err := http.Get("http://127.0.0.1" + listen + "/healthz")
		if err != nil || resp.StatusCode != http.StatusNoContent {
			os.Exit(1)
		}
		return
	}

	timeout := duration(logger, "INGOT_IDLE_TIMEOUT", idle.DefaultTimeout)
	engine := idle.NewSocketEngine(envOr("INGOT_IDLE_DOCKER_SOCKET", "/var/run/docker.sock"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	watcher := &idle.Watcher{
		Engine:        engine,
		Tracker:       idle.NewTracker(idle.DefaultThresholds),
		Interval:      duration(logger, "INGOT_IDLE_INTERVAL", 30*time.Second),
		Timeout:       timeout,
		BusyWarnAfter: duration(logger, "INGOT_IDLE_BUSY_WARN", 2*time.Hour),
		Log:           logger,
	}
	if watcher.Interval <= 0 {
		watcher.Interval = 30 * time.Second
	}
	go watcher.Run(ctx)

	server := &http.Server{
		Addr: listen,
		Handler: &idle.Waker{
			Engine:      engine,
			WakeTimeout: duration(logger, "INGOT_IDLE_WAKE_TIMEOUT", 60*time.Second),
			Log:         logger,
		},
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()

	if timeout > 0 {
		logger.Printf("stopping projects after %s idle; waking them on %s", timeout, listen)
	} else {
		logger.Printf("idle stopping is off (except projects with their own timeout); waking projects on %s", listen)
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal(err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func duration(logger *log.Logger, key string, def time.Duration) time.Duration {
	d, err := idle.ParseTimeout(os.Getenv(key), def)
	if err != nil {
		logger.Printf("ignoring invalid %s=%q, using %s", key, os.Getenv(key), def)
		return def
	}
	return d
}
