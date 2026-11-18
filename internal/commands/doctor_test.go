package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aliasproject/ingot/internal/docker"
	"github.com/aliasproject/ingot/internal/utils"
)

func TestParseGetentAhosts(t *testing.T) {
	out := "::1             STREAM ingot-doctor-probe.test\n" +
		"::1             DGRAM  \n" +
		"::1             RAW    \n" +
		"127.0.0.1       STREAM \n" +
		"127.0.0.1       DGRAM  \n" +
		"127.0.0.1       RAW    \n"
	if got, want := parseGetentAhosts(out), []string{"::1", "127.0.0.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parseGetentAhosts = %v, want %v", got, want)
	}
	if got := parseGetentAhosts(""); len(got) != 0 {
		t.Fatalf("parseGetentAhosts(\"\") = %v, want none", got)
	}
}

// localhost is in /etc/hosts everywhere, so it resolves through getent
// without touching DNS.
func TestLookupSystemUsesGetent(t *testing.T) {
	if runtime.GOOS != "linux" || !utils.CommandExists("getent") {
		t.Skip("needs Linux with getent")
	}
	addrs, err := lookupSystem(context.Background(), "localhost")
	if err != nil || len(addrs) == 0 {
		t.Fatalf("lookupSystem(localhost) = %v, %v", addrs, err)
	}
	if _, err := lookupSystem(context.Background(), "ingot-no-such-host.invalid"); err == nil {
		t.Fatal("lookupSystem of an unknown name succeeded")
	}
}

// A getent that fails for a reason other than "not found" (exit 2) must not
// make doctor report the name as unresolvable; Go's resolver is asked instead.
func TestLookupSystemFallsBackWhenGetentBreaks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("getent path is Linux-only")
	}
	fake := func(code string) {
		path := filepath.Join(t.TempDir(), "getent")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit "+code+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		getentCommand = path
	}
	defer func(orig string) { getentCommand = orig }(getentCommand)

	fake("1")
	if addrs, err := lookupSystem(context.Background(), "localhost"); err != nil || len(addrs) == 0 {
		t.Fatalf("getent exit 1: lookupSystem(localhost) = %v, %v; want Go resolver fallback", addrs, err)
	}
	fake("2")
	if _, err := lookupSystem(context.Background(), "localhost"); err == nil {
		t.Fatal("getent exit 2 (not found): lookupSystem succeeded, want an error")
	}
}

func TestDockerPermissionFixes(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux fixes")
	}
	fixes := strings.Join(DockerPermissionFixes(""), "\n")
	for _, want := range []string{"usermod -aG docker", "newgrp docker", "setfacl -m u:$USER:rw /var/run/docker.sock", "DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock"} {
		if !strings.Contains(fixes, want) {
			t.Errorf("DockerPermissionFixes(\"\") missing %q:\n%s", want, fixes)
		}
	}
	if got := strings.Join(DockerPermissionFixes("/run/custom.sock"), "\n"); !strings.Contains(got, "rw /run/custom.sock") {
		t.Errorf("DockerPermissionFixes should name the socket the CLI tried, got:\n%s", got)
	}
}

func TestDockerFixForPicksPermissionFixes(t *testing.T) {
	denied := fmt.Errorf("%w: permission denied while trying to connect to the docker API at unix:///var/run/docker.sock", docker.ErrPermissionDenied)
	if got := dockerFixFor(denied); got == dockerFix() || !strings.Contains(got, "/var/run/docker.sock") {
		t.Errorf("dockerFixFor(permission denied) = %q, want the socket access fixes", got)
	}
	notRunning := fmt.Errorf("%w: connection refused", docker.ErrDockerNotRunning)
	if got := dockerFixFor(notRunning); got != dockerFix() {
		t.Errorf("dockerFixFor(not running) = %q, want %q", got, dockerFix())
	}
}
