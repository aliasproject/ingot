package project

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
)

// HasDockerfile checks whether the project root contains a Dockerfile at the
// default location.
func HasDockerfile(projectPath string) bool {
	_, err := os.Stat(filepath.Join(projectPath, "Dockerfile"))
	return err == nil
}

var argLineRe = regexp.MustCompile(`(?i)^\s*ARG\s+([A-Za-z_][A-Za-z0-9_]*)\s*(=.*)?$`)

// DetectBuildArgs scans a Dockerfile for ARG declarations that have no
// default value (e.g. "ARG GITHUB_TOKEN", not "ARG APP_UID=1000") - those are
// exactly the ones a build can't proceed without a value for, and the ones
// worth asking about. Returns names in the order they appear; never errors on
// a missing file, just returns an empty slice.
func DetectBuildArgs(dockerfilePath string) []string {
	f, err := os.Open(dockerfilePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var names []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		matches := argLineRe.FindStringSubmatch(scanner.Text())
		if matches == nil {
			continue
		}
		if matches[2] != "" {
			// Has a default value (e.g. "=1000") - no need to prompt.
			continue
		}
		names = append(names, matches[1])
	}
	return names
}

// secretArgRe matches build-arg names that look like credentials, e.g.
// GITHUB_TOKEN, NPM_AUTH, DB_PASSWORD, COMPOSER_AUTH, SSH_KEY.
var secretArgRe = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|AUTH|CREDENTIAL|PRIVATE|(^|_)KEY$)`)

// IsSecretBuildArg reports whether a build arg's value should be treated as
// a credential: never stored in a container label, and asked for again (or
// read from the same-named env var) when the image is rebuilt.
func IsSecretBuildArg(name string) bool {
	return secretArgRe.MatchString(name)
}

// DetectFramework inspects the project root for markers of a known framework.
// Currently only WordPress is detected (wp-config.php covers stock WordPress
// and Bedrock; wp-load.php covers stock WordPress installs that haven't been
// configured yet). Returns "" when nothing is recognized.
func DetectFramework(projectPath string) string {
	for _, marker := range []string{"wp-config.php", "wp-load.php"} {
		if _, err := os.Stat(filepath.Join(projectPath, marker)); err == nil {
			return "wordpress"
		}
	}
	return ""
}
