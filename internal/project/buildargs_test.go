package project

import (
	"strings"
	"testing"
)

func TestIsSecretBuildArg(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "github_token", "COMPOSER_AUTH", "DB_PASSWORD", "APP_SECRET", "SSH_KEY", "KEY", "NPM_AUTH_TOKEN", "PRIVATE_REPO_URL", "GH_PAT", "REGISTRY_PASS", "APIKEY", "API_KEY", "NPMRC", "AUTH_HEADER", "SSH_PASSPHRASE", "GITHUB_OAUTH", "AUTHORIZATION", "DB_PWD"} {
		if !IsSecretBuildArg(name) {
			t.Errorf("IsSecretBuildArg(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"APP_UID", "PHP_VERSION", "NODE_ENV", "KEYBOARD_LAYOUT", "MONKEY_NAME", "AUTHOR", "PATH_PREFIX", "PASSPORT_URL", "COMPASS", "SSH_AUTH_SOCK"} {
		if IsSecretBuildArg(name) {
			t.Errorf("IsSecretBuildArg(%q) = true, want false", name)
		}
	}
}

func TestSerializeBuildArgsDropsSecretValues(t *testing.T) {
	label := serializeBuildArgs(map[string]string{
		"GITHUB_TOKEN": "gho_abc123",
		"APP_UID":      "1000",
	})
	if strings.Contains(label, "gho_abc123") {
		t.Fatalf("label %q contains the token value", label)
	}
	if label != "APP_UID=1000,GITHUB_TOKEN" {
		t.Fatalf("label = %q, want %q", label, "APP_UID=1000,GITHUB_TOKEN")
	}

	args := parseBuildArgs(label)
	if args["APP_UID"] != "1000" {
		t.Errorf("APP_UID = %q, want 1000", args["APP_UID"])
	}
	if value, ok := args["GITHUB_TOKEN"]; !ok || value != "" {
		t.Errorf("GITHUB_TOKEN = %q (present %v), want present and empty", value, ok)
	}
}

// Labels written before ALI-837 still hold KEY=VALUE for secret args. The
// value must not be reused (it should be rotated), and doctor must see it.
func TestLegacySecretBuildArgLabels(t *testing.T) {
	labels := map[string]string{"ingot.project.build_args": "GITHUB_TOKEN=gho_abc123,APP_UID=1000"}
	args := parseBuildArgs(labels["ingot.project.build_args"])
	if args["GITHUB_TOKEN"] != "" {
		t.Fatalf("GITHUB_TOKEN = %q, want the legacy value dropped", args["GITHUB_TOKEN"])
	}
	if args["APP_UID"] != "1000" {
		t.Errorf("APP_UID = %q, want 1000", args["APP_UID"])
	}
	if strings.Contains(serializeBuildArgs(args), "gho_abc123") {
		t.Fatal("re-serializing a legacy label kept the token value")
	}
	if got := StoredSecretBuildArgs(labels); len(got) != 1 || got[0] != "GITHUB_TOKEN" {
		t.Fatalf("StoredSecretBuildArgs = %v, want [GITHUB_TOKEN]", got)
	}
	if got := StoredSecretBuildArgs(map[string]string{"ingot.project.build_args": "APP_UID=1000,GITHUB_TOKEN"}); len(got) != 0 {
		t.Fatalf("StoredSecretBuildArgs on a fixed label = %v, want none", got)
	}
}

func TestResolveBuildArgsReadsSecretsFromEnv(t *testing.T) {
	t.Setenv("INGOT_BUILD_ARG_GITHUB_TOKEN", "from_env")
	resolved := resolveBuildArgs(map[string]string{"GITHUB_TOKEN": "", "APP_UID": "1000"})
	if resolved["GITHUB_TOKEN"] != "from_env" {
		t.Errorf("GITHUB_TOKEN = %q, want from_env", resolved["GITHUB_TOKEN"])
	}
	if resolved["APP_UID"] != "1000" {
		t.Errorf("APP_UID = %q, want 1000", resolved["APP_UID"])
	}
}

// A bare non-secret fragment in a label is the tail of a value that had a
// comma in it, not an arg.
func TestParseBuildArgsSkipsCommaFragments(t *testing.T) {
	args := parseBuildArgs("FOO=a,b,GITHUB_TOKEN")
	if _, ok := args["b"]; ok {
		t.Errorf("parsed bogus arg %q from a comma in a value: %v", "b", args)
	}
	if _, ok := args["GITHUB_TOKEN"]; !ok {
		t.Errorf("bare secret key GITHUB_TOKEN missing: %v", args)
	}
}

func TestSplitBuildArgs(t *testing.T) {
	plain, secret := splitBuildArgs(map[string]string{"GITHUB_TOKEN": "t", "APP_UID": "1000"})
	if len(plain) != 1 || plain["APP_UID"] != "1000" {
		t.Errorf("plain = %v, want only APP_UID", plain)
	}
	if len(secret) != 1 || secret["GITHUB_TOKEN"] != "t" {
		t.Errorf("secret = %v, want only GITHUB_TOKEN", secret)
	}
}

// A legacy label whose secret value was JSON with commas in it must not
// turn a piece of the secret into a plain arg.
func TestParseBuildArgsDropsSecretTails(t *testing.T) {
	args := parseBuildArgs(`COMPOSER_AUTH={"a":"b","c":"d==",APP_UID=1000`)
	if len(args) != 2 || args["COMPOSER_AUTH"] != "" || args["APP_UID"] != "1000" {
		t.Fatalf("args = %v, want only COMPOSER_AUTH (empty) and APP_UID", args)
	}
}

func TestSecretBuildArgFromEnvUpperCases(t *testing.T) {
	t.Setenv("INGOT_BUILD_ARG_GITHUB_TOKEN", "v")
	if got := SecretBuildArgFromEnv("github_token"); got != "v" {
		t.Fatalf("SecretBuildArgFromEnv(github_token) = %q, want v", got)
	}
}
