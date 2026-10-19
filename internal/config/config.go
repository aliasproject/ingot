package config

import (
	"os"
	"strings"
	"sync"
)

const (
	// Version is the current version of Ingot
	Version = "2.0.0"

	// ClientID is the GitHub OAuth application client ID
	ClientID = "178c6fc778ccc68e1d6a"

	// DefaultPHPVersion is the default PHP version for new projects
	DefaultPHPVersion = "8.4"

	// IngotBinary is the installation path for the ingot binary
	IngotBinary = "/usr/local/bin/ingot"

	// IdleImage is the ingot_idle service's image, tagged with the CLI
	// version so each CLI runs the service built from the same source.
	IdleImage = "ghcr.io/aliasproject/ingot-idle:" + Version

	// OpensslImage generates the CA and per-domain certificates
	// (Dockerfile.openssl), tagged with the CLI version for the same reason:
	// the CLI relies on the file names and certificate layout it writes.
	OpensslImage = "ghcr.io/aliasproject/ingot-openssl:" + Version
)

// Config holds the application configuration
type Config struct {
	mu sync.RWMutex

	// Settings (loaded from environment variables)
	Databases         map[string]DatabaseConfig
	GithubToken       string
	DefaultPHPVersion string
	AutoStartGlobal   bool

	// Network
	HostIP  string
	DNSPort string

	// Features
	EnableSSL bool
	EnableDNS bool

	// Sharing (ingot share). When both are set, shares use a named Cloudflare
	// Tunnel with stable <project>.<ShareDomain> hostnames instead of a
	// throwaway trycloudflare.com quick tunnel.
	ShareTunnelToken string
	ShareDomain      string

	// IdleTimeout is how long a project may sit idle before ingot_idle stops
	// it (INGOT_IDLE_TIMEOUT: a Go duration such as "30m", or "off"). Empty
	// means the service's own default. Passed through to the service as-is;
	// it validates the value.
	IdleTimeout string
}

// ShareNamedMode reports whether ingot share should use a named tunnel.
func (c *Config) ShareNamedMode() bool {
	return c.ShareTunnelToken != "" && c.ShareDomain != ""
}

// DatabaseConfig holds configuration for a database instance
type DatabaseConfig struct {
	Type    string `json:"type"`    // mysql or postgresql
	Version string `json:"version"` // e.g., 8.0, 16
	Port    string `json:"port"`    // external port
}

var (
	instance *Config
	once     sync.Once
)

// Get returns the singleton config instance
func Get() *Config {
	once.Do(func() {
		instance = &Config{}
		instance.initialize()
	})
	return instance
}

// initialize sets up the config with default values and loads from file
func (c *Config) initialize() {
	// Set defaults
	c.DefaultPHPVersion = DefaultPHPVersion
	c.DNSPort = "53"
	c.EnableSSL = true
	c.EnableDNS = true
	c.AutoStartGlobal = true
	c.Databases = make(map[string]DatabaseConfig)

	// Network detection - use default or environment variable
	if hostIP := os.Getenv("INGOT_HOST_IP"); hostIP != "" {
		c.HostIP = hostIP
	} else {
		c.HostIP = "127.0.0.1" // default
	}

	// Load all configuration from environment variables
	c.loadFromEnvironment()
}

// Load reads configuration from file
func (c *Config) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only load from environment variables - no config file
	c.loadFromEnvironment()

	return nil
}

// Save is a no-op since we don't save configuration locally
func (c *Config) Save() error {
	return nil
}

// loadFromEnv overrides config values with environment variables
func (c *Config) loadFromEnv() {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		c.GithubToken = token
	}

	if phpVersion := os.Getenv("DEFAULT_PHP_VERSION"); phpVersion != "" {
		c.DefaultPHPVersion = phpVersion
	}
}

// SetGithubToken sets the GitHub token in environment variable
func (c *Config) SetGithubToken(token string) error {
	// Set environment variable
	os.Setenv("GITHUB_TOKEN", token)
	return nil
}

// AddDatabase adds a database configuration and saves
func (c *Config) AddDatabase(name, dbType, version, port string) error {
	c.mu.Lock()
	if c.Databases == nil {
		c.Databases = make(map[string]DatabaseConfig)
	}
	c.Databases[name] = DatabaseConfig{
		Type:    dbType,
		Version: version,
		Port:    port,
	}
	c.mu.Unlock()

	return c.Save()
}

// RemoveDatabase removes a database configuration and saves
func (c *Config) RemoveDatabase(name string) error {
	c.mu.Lock()
	delete(c.Databases, name)
	c.mu.Unlock()

	return nil
}

// loadFromEnvironment loads configuration from environment variables
func (c *Config) loadFromEnvironment() {
	// Default PHP version
	if version := os.Getenv("INGOT_DEFAULT_PHP_VERSION"); version != "" {
		c.DefaultPHPVersion = version
	}

	// Network settings
	if hostIP := os.Getenv("INGOT_HOST_IP"); hostIP != "" {
		c.HostIP = hostIP
	}

	if dnsPort := os.Getenv("INGOT_DNS_PORT"); dnsPort != "" {
		c.DNSPort = dnsPort
	}

	// Feature flags
	if enableSSL := os.Getenv("INGOT_ENABLE_SSL"); enableSSL != "" {
		c.EnableSSL = enableSSL == "true" || enableSSL == "1"
	}

	if enableDNS := os.Getenv("INGOT_ENABLE_DNS"); enableDNS != "" {
		c.EnableDNS = enableDNS == "true" || enableDNS == "1"
	}

	// Auto start global services
	if autoStart := os.Getenv("INGOT_AUTO_START_GLOBAL"); autoStart != "" {
		c.AutoStartGlobal = autoStart == "true" || autoStart == "1"
	}

	// Named-tunnel sharing
	c.ShareTunnelToken = os.Getenv("INGOT_SHARE_TUNNEL_TOKEN")
	c.ShareDomain = normalizeShareDomain(os.Getenv("INGOT_SHARE_DOMAIN"))

	// Scale to zero
	c.IdleTimeout = strings.TrimSpace(os.Getenv("INGOT_IDLE_TIMEOUT"))
}

// normalizeShareDomain accepts the forms people paste - "*.acme-dev.com" (the
// hostname Cloudflare's dashboard is set up with), "https://acme-dev.com/",
// ".acme-dev.com" - and reduces them to the bare domain.
func normalizeShareDomain(value string) string {
	d := strings.ToLower(strings.TrimSpace(value))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	d = strings.TrimSuffix(d, "/")
	d = strings.TrimPrefix(d, "*")
	return strings.Trim(d, ".")
}

// ShareDomainValid reports whether ShareDomain is a plausible hostname, so
// doctor can flag a typo before Caddy rejects the vhost built from it.
func (c *Config) ShareDomainValid() bool {
	if c.ShareDomain == "" || !strings.Contains(c.ShareDomain, ".") {
		return false
	}
	for _, r := range c.ShareDomain {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// GetGithubToken returns the GitHub token from environment variable
func (c *Config) GetGithubToken() string {
	return os.Getenv("GITHUB_TOKEN")
}

// GetDatabases returns all database configurations
func (c *Config) GetDatabases() map[string]DatabaseConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	databases := make(map[string]DatabaseConfig)
	for k, v := range c.Databases {
		databases[k] = v
	}
	return databases
}

// IsInstalled checks if Ingot is installed
func (c *Config) IsInstalled() bool {
	_, err := os.Stat(IngotBinary)
	return err == nil
}

// ProjectConfig is no longer needed - all configuration is ephemeral or stored in Docker labels

// LoadProjectConfig and SaveProjectConfig are no longer needed
// All project configuration is now ephemeral or stored in Docker container labels

// NetworkConfig holds network configuration
type NetworkConfig struct {
	BridgeName  string `json:"bridge_name"`
	InternalNet string `json:"internal_net"`
}

// GetNetworkConfig returns the default network configuration
func GetNetworkConfig() *NetworkConfig {
	return &NetworkConfig{
		BridgeName:  "ingot_internal",
		InternalNet: "ingot_internal",
	}
}
