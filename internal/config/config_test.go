package config

import "testing"

func TestNormalizeShareDomain(t *testing.T) {
	for in, want := range map[string]string{
		"acme-dev.com":          "acme-dev.com",
		" *.acme-dev.com ":      "acme-dev.com",
		"https://acme-dev.com/": "acme-dev.com",
		".Acme-Dev.com":         "acme-dev.com",
	} {
		if got := normalizeShareDomain(in); got != want {
			t.Errorf("normalizeShareDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
