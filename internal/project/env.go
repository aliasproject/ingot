package project

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile reads a dotenv-format .env file from the project root, if one
// exists. Mirrors how production Alios supplies a site's config - as plain
// KEY=VALUE pairs injected on `docker run` (see RunDockerContainer's
// buildEnvironmentVariables in the Alios platform repo) - except the source
// is the project's own .env file rather than a database-backed setting,
// since this CLI has no database. Returns (nil, nil) when there's no .env
// file: support is opt-in by the file's presence, not a new default every
// project has to think about.
func LoadEnvFile(projectPath string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(projectPath, ".env"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseEnvFile(data), nil
}

// EnvFileHash returns a short, stable hex digest of the project's .env file
// content, or "" if there's no .env file. Used to detect when .env has
// changed since the container was created, so Start can recreate the
// container the same way a PHP-version change already does - matching how
// production's own deploy path (RunAndSwapContainer) always recreates the
// container on any config change too, never patches one in place.
func EnvFileHash(projectPath string) string {
	data, err := os.ReadFile(filepath.Join(projectPath, ".env"))
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseEnvFile(data []byte) map[string]string {
	vars := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			first, last := value[0], value[len(value)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		vars[key] = value
	}
	return vars
}
