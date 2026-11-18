package docker

import (
	"errors"
	"strings"
	"testing"
)

func TestDaemonErrorClassifiesPermissionDenied(t *testing.T) {
	exitErr := errors.New("exit status 1")
	cases := []struct {
		name, stderr string
		want         error
		notWant      error
	}{
		{
			name:    "socket not accessible",
			stderr:  "permission denied while trying to connect to the docker API at unix:///var/run/docker.sock\n",
			want:    ErrPermissionDenied,
			notWant: ErrDockerNotRunning,
		},
		{
			name:    "older CLI wording",
			stderr:  "Got permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock: Get \"http://%2Fvar%2Frun%2Fdocker.sock/v1.24/version\": dial unix /var/run/docker.sock: connect: permission denied\n",
			want:    ErrPermissionDenied,
			notWant: ErrDockerNotRunning,
		},
		{
			name:    "socket missing",
			stderr:  "failed to connect to the docker API at unix:///var/run/docker.sock; check if the path is correct and if the daemon is running: dial unix /var/run/docker.sock: connect: no such file or directory\n",
			want:    ErrDockerNotRunning,
			notWant: ErrPermissionDenied,
		},
		{
			name:    "connection refused",
			stderr:  "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?\n",
			want:    ErrDockerNotRunning,
			notWant: ErrPermissionDenied,
		},
		{
			name:    "no stderr (timeout)",
			stderr:  "",
			want:    ErrDockerNotRunning,
			notWant: ErrPermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := daemonError(exitErr, tc.stderr)
			if !errors.Is(err, tc.want) {
				t.Errorf("daemonError = %v, want it to wrap %v", err, tc.want)
			}
			if errors.Is(err, tc.notWant) {
				t.Errorf("daemonError = %v, must not wrap %v", err, tc.notWant)
			}
			if tc.stderr != "" && !strings.Contains(err.Error(), strings.TrimSpace(strings.SplitN(tc.stderr, "\n", 2)[0])) {
				t.Errorf("daemonError = %v, want the CLI's message kept as detail", err)
			}
		})
	}
}

func TestSocketFromError(t *testing.T) {
	cases := map[string]string{
		"permission denied while trying to connect to the docker API at unix:///var/run/docker.sock":        "/var/run/docker.sock",
		"failed to connect to the docker API at unix:///run/user/1000/docker.sock; check if the path":       "/run/user/1000/docker.sock",
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?": "/var/run/docker.sock",
		"error during connect: tcp://10.0.0.2:2376":                                                         "",
	}
	for msg, want := range cases {
		if got := SocketFromError(errors.New(msg)); got != want {
			t.Errorf("SocketFromError(%q) = %q, want %q", msg, got, want)
		}
	}
	if got := SocketFromError(nil); got != "" {
		t.Errorf("SocketFromError(nil) = %q, want \"\"", got)
	}
}
