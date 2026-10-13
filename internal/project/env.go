package project

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// envLabel is the container label that carries a project's user-supplied
// env vars. Stored as a JSON object rather than the comma-joined KEY=VALUE
// format build_args uses, because env values routinely contain commas and
// equals signs. Like every other per-project setting, this lives on the
// container itself, not in a file on disk (see CLAUDE.md's "no persisted
// config files" invariant).
const envLabel = "ingot.project.env"

// reservedEnvKeys are the infra vars createContainer always sets itself, last,
// so a user var can never override what ingot needs to route traffic to the
// container. Rejected up front by SetEnv rather than silently clobbered.
var reservedEnvKeys = map[string]bool{
	"SERVER_NAME": true,
	"SERVER_ROOT": true,
	"HTTPS":       true,
	"PORT":        true,
}

// IsReservedEnvKey reports whether key is one of ingot's own infra vars.
func IsReservedEnvKey(key string) bool {
	return reservedEnvKeys[key]
}

// ParseEnvPair splits a KEY=VALUE argument as given on the command line.
// The value may be empty (`FOO=`) and may itself contain `=`; only the first
// one separates key from value.
func ParseEnvPair(arg string) (key, value string, err error) {
	key, value, found := strings.Cut(arg, "=")
	if !found {
		return "", "", fmt.Errorf("invalid env var %q: expected KEY=VALUE", arg)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("invalid env var %q: key cannot be empty", arg)
	}
	if strings.ContainsAny(key, " \t\n") {
		return "", "", fmt.Errorf("invalid env var %q: key cannot contain whitespace", arg)
	}
	return key, value, nil
}

// SortedEnvKeys returns the map's keys in stable order, for display.
func SortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func serializeEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	data, err := json.Marshal(env)
	if err != nil {
		// A map[string]string can't fail to marshal; keep the signature simple.
		return ""
	}
	return string(data)
}

func parseEnv(encoded string) map[string]string {
	if encoded == "" {
		return nil
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(encoded), &env); err != nil {
		return nil
	}
	if len(env) == 0 {
		return nil
	}
	return env
}
