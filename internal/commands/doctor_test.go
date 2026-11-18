package commands

import (
	"context"
	"fmt"
	"net"
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

	// No rootless daemon: ACL first, docker group last with its warning.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	fixes := DockerPermissionFixes("")
	if len(fixes) != 2 {
		t.Fatalf("DockerPermissionFixes(\"\") = %q, want ACL and group fixes only", fixes)
	}
	if !strings.Contains(fixes[0], "setfacl -m u:$USER:rw /var/run/docker.sock") {
		t.Errorf("first fix = %q, want the setfacl ACL", fixes[0])
	}
	for _, want := range []string{"usermod -aG docker", "newgrp docker", "root-equivalent"} {
		if !strings.Contains(fixes[1], want) {
			t.Errorf("last fix = %q, missing %q", fixes[1], want)
		}
	}
	if got := strings.Join(DockerPermissionFixes("/run/custom.sock"), "\n"); !strings.Contains(got, "rw /run/custom.sock") {
		t.Errorf("DockerPermissionFixes should name the socket the CLI tried, got:\n%s", got)
	}

	// A regular file where the rootless socket would be isn't a daemon.
	rt := t.TempDir()
	rootless := filepath.Join(rt, "docker.sock")
	if err := os.WriteFile(rootless, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	if got := strings.Join(DockerPermissionFixes("/var/run/docker.sock"), "\n"); strings.Contains(got, "DOCKER_HOST") {
		t.Errorf("rootless hint shown for a non-socket file:\n%s", got)
	}

	// A rootless socket exists: pointing DOCKER_HOST at it comes first.
	if err := os.Remove(rootless); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", rootless)
	if err != nil {
		t.Skipf("can't create a unix socket here: %v", err)
	}
	defer l.Close()
	fixes = DockerPermissionFixes("/var/run/docker.sock")
	if len(fixes) != 3 || !strings.Contains(fixes[0], "DOCKER_HOST=unix://"+rootless) ||
		!strings.Contains(fixes[1], "setfacl") || !strings.Contains(fixes[2], "usermod") {
		t.Errorf("with a rootless socket, fixes = %q, want DOCKER_HOST, setfacl, usermod in that order", fixes)
	}

	// The denied socket is the rootless one: don't suggest it again,
	// however the CLI spelled the path.
	for _, denied := range []string{rootless, filepath.Join(rt, ".", "docker.sock") + "", rt + "//docker.sock"} {
		if got := strings.Join(DockerPermissionFixes(denied), "\n"); strings.Contains(got, "DOCKER_HOST") {
			t.Errorf("rootless hint shown for denied rootless socket %q:\n%s", denied, got)
		}
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
