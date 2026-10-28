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
