package docker

import (
	"strings"
	"testing"
)

func TestBuildCommandKeepsSecretsOutOfArgv(t *testing.T) {
	args, env := buildCommand(&BuildConfig{
		Tag:            "local/p:latest",
		DockerfilePath: "/p/Dockerfile",
		Context:        "/p",
		BuildArgs:      map[string]string{"APP_UID": "1000"},
		SecretArgs:     map[string]string{"GITHUB_TOKEN": "gho_abc123"},
	})
	argv := strings.Join(args, " ")
	if strings.Contains(argv, "gho_abc123") {
		t.Fatalf("argv %q contains the secret", argv)
	}
	want := "build -f /p/Dockerfile -t local/p:latest --build-arg APP_UID=1000 --build-arg GITHUB_TOKEN /p"
	if argv != want {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if len(env) != 1 || env[0] != "GITHUB_TOKEN=gho_abc123" {
		t.Errorf("env = %v, want [GITHUB_TOKEN=gho_abc123]", env)
	}
}
