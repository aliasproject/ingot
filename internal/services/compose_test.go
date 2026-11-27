package services

import (
	"strings"
	"testing"

	"github.com/aliasproject/ingot/internal/config"
)

// No service may be pinned to a platform: every image Ingot runs is
// multi-arch, and an amd64 pin makes the container unrunnable on arm64
// Linux hosts without QEMU.
func TestComposeHasNoPlatformPins(t *testing.T) {
	m := &Manager{config: &config.Config{}}
	if err := m.config.AddDatabase("mysql", "mysql", "8.0", "3306"); err != nil {
		t.Fatal(err)
	}
	if err := m.config.AddDatabase("postgresql", "postgresql", "16", "5432"); err != nil {
		t.Fatal(err)
	}
	compose := m.generateComposeConfig("127.0.0.1")

	for _, want := range []string{"image: mysql:8.0\n", "image: postgres:16\n", "image: axllent/mailpit:"} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose config is missing %q:\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "platform:") {
		t.Errorf("compose config pins a platform:\n%s", compose)
	}
}

// Mailpit accepts any SMTP credentials and the databases have empty or
// well-known passwords, so their ports are published on localhost only,
// never on every interface (ALI-1242).
func TestComposePublishesMailAndDatabasesOnLocalhostOnly(t *testing.T) {
	m := &Manager{config: &config.Config{}}
	if err := m.config.AddDatabase("mysql", "mysql", "8.0", "3307"); err != nil {
		t.Fatal(err)
	}
	if err := m.config.AddDatabase("postgresql", "postgresql", "16", "5433"); err != nil {
		t.Fatal(err)
	}
	compose := m.generateComposeConfig("192.0.2.10")

	for _, want := range []string{
		`- "127.0.0.1:1025:1025"`,
		`- "127.0.0.1:8025:8025"`,
		`- "127.0.0.1:3307:3306"`,
		`- "127.0.0.1:5433:5432"`,
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("compose config is missing %s:\n%s", want, compose)
		}
	}
	for _, unwanted := range []string{`- "1025:1025"`, `- "8025:8025"`, `- "3307:3306"`, `- "5433:5432"`, "192.0.2.10:1025", "192.0.2.10:8025", "192.0.2.10:3307", "192.0.2.10:5433"} {
		if strings.Contains(compose, unwanted) {
			t.Errorf("compose config publishes %s beyond localhost:\n%s", unwanted, compose)
		}
	}
}

// Caddy needs a snippet declared before the vhosts that import it, and the
// page it serves must be written to the path the snippet reads.
func TestBaseCaddyfileEmpty500Snippet(t *testing.T) {
	snippet := strings.Index(baseCaddyfile, "(ingot_empty_500) {")
	vhosts := strings.Index(baseCaddyfile, "import /etc/caddy/*.caddyfile")
	if snippet < 0 || snippet > vhosts {
		t.Fatalf("ingot_empty_500 must be defined before the vhost import:\n%s", baseCaddyfile)
	}
	body := baseCaddyfile[snippet:vhosts]
	for _, want := range []string{
		"status 500\n",            // not 5xx: 502-504 belong to ingot_wake
		"header Content-Length 0", // a 500 with a body passes through
		"root * /etc/caddy/errors",
		"rewrite * /empty_500.html",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ingot_empty_500 snippet is missing %q:\n%s", want, body)
		}
	}

	page := string(empty500Page)
	for _, want := range []string{"<h1>", `{{placeholder "http.vars.ingot_project" | html}}`, "ingot logs", "composer install", "APP_KEY"} {
		if !strings.Contains(page, want) {
			t.Errorf("empty 500 page is missing %q", want)
		}
	}
}
