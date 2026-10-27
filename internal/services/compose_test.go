package services

import (
	"strings"
	"testing"

	"github.com/aliasproject/ingot/internal/config"
)

// serviceBlock returns the compose YAML for one service, from its
// "  <name>:" line up to the next service or top-level key.
func serviceBlock(t *testing.T, compose, name string) string {
	t.Helper()
	start := strings.Index(compose, "\n  "+name+":\n")
	if start < 0 {
		t.Fatalf("service %q not in compose config:\n%s", name, compose)
	}
	rest := compose[start+1:]
	lines := strings.Split(rest, "\n")
	end := len(lines)
	for i, line := range lines[1:] {
		if line != "" && !strings.HasPrefix(line, "   ") {
			end = i + 1
			break
		}
	}
	return strings.Join(lines[:end], "\n")
}

// Only images that ship no arm64 build may be pinned to amd64: a pin makes
// the container unrunnable on arm64 Linux hosts without QEMU.
func TestComposePlatformPins(t *testing.T) {
	for _, tc := range []struct {
		version string
		pinned  bool
	}{
		{"8.0", false},
		{"5.7", true},
	} {
		m := &Manager{config: &config.Config{}}
		if err := m.config.AddDatabase("mysql", "mysql", tc.version, "3306"); err != nil {
			t.Fatal(err)
		}
		compose := m.generateComposeConfig("127.0.0.1")

		mysql := serviceBlock(t, compose, "mysql")
		if got := strings.Contains(mysql, "platform: linux/amd64"); got != tc.pinned {
			t.Errorf("mysql:%s pinned to amd64 = %v, want %v:\n%s", tc.version, got, tc.pinned, mysql)
		}
		if !strings.Contains(mysql, "image: mysql:"+tc.version+"\n") {
			t.Errorf("mysql:%s image line malformed:\n%s", tc.version, mysql)
		}

		mail := serviceBlock(t, compose, "mail")
		if strings.Contains(mail, "platform:") {
			t.Errorf("mail service is platform-pinned:\n%s", mail)
		}
	}
}
