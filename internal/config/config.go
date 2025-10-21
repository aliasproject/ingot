package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// Version is the current version of Devbox
	Version = "0.9.6"

	// ClientID is the GitHub OAuth application client ID
	ClientID = "178c6fc778ccc68e1d6a"

	// DefaultPHPVersion is the default PHP version for new projects
	DefaultPHPVersion = "8.3"

	// DevboxBinary is the installation path for the devbox binary
	DevboxBinary = "/usr/local/bin/devbox"
)

// Config holds the application configuration
type Config struct {
	mu sync.RWMutex

	// Paths
	HomeDir    string `json:"-"`
	DevboxDir  string `json:"-"`
	ConfigDir  string `json:"-"`
	CertsDir   string `json:"-"`
	CAPath     string `json:"-"`
	ConfigFile string `json:"-"`

	// Settings
	Databases         map[string]DatabaseConfig `json:"databases,omitempty"`
	GithubToken       string                    `json:"github_token,omitempty"`
	DefaultPHPVersion string                    `json:"default_php_version,omitempty"`
	AutoStartGlobal   bool                      `json:"auto_start_global,omitempty"`

	// Network
	HostIP  string `json:"host_ip,omitempty"`
	DNSPort string `json:"dns_port,omitempty"`

	// Features
	EnableSSL bool `json:"enable_ssl,omitempty"`
	EnableDNS bool `json:"enable_dns,omitempty"`
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
	c.HomeDir = os.Getenv("HOME")
	if c.HomeDir == "" {
		c.HomeDir = os.Getenv("USERPROFILE") // Windows fallback
	}

	c.DevboxDir = filepath.Join(c.HomeDir, ".devbox")
	c.ConfigDir = c.DevboxDir
	c.CertsDir = filepath.Join(c.DevboxDir, "certs")
	c.CAPath = filepath.Join(c.CertsDir, "AliasProjectCASelfSigned.pem")
	c.ConfigFile = filepath.Join(c.DevboxDir, "config.json")

	// Set defaults
	c.DefaultPHPVersion = DefaultPHPVersion
	c.AutoStartGlobal = true
	c.EnableSSL = true
	c.EnableDNS = true
	c.DNSPort = "5354"
	c.Databases = make(map[string]DatabaseConfig)

	// Create directories if they don't exist
	os.MkdirAll(c.DevboxDir, 0755)
	os.MkdirAll(c.CertsDir, 0755)

	// Load from file if exists
	c.Load()

	// Override with environment variables
	c.loadFromEnv()
}

// Load reads configuration from file
func (c *Config) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, err := os.Stat(c.ConfigFile); os.IsNotExist(err) {
		return nil // No config file yet
	}

	data, err := os.ReadFile(c.ConfigFile)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, c); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	return nil
}

// Save writes configuration to file
func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(c.ConfigFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

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

// SetGithubToken sets the GitHub token and saves config
func (c *Config) SetGithubToken(token string) error {
	c.mu.Lock()
	c.GithubToken = token
	c.mu.Unlock()

	os.Setenv("GITHUB_TOKEN", token)
	return c.Save()
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

	return c.Save()
}

// GetGithubToken returns the GitHub token
func (c *Config) GetGithubToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.GithubToken
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

// IsInstalled checks if Devbox is installed
func (c *Config) IsInstalled() bool {
	_, err := os.Stat(DevboxBinary)
	return err == nil
}

// ProjectConfig holds project-specific configuration
type ProjectConfig struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	PHPVersion  string            `json:"php_version"`
	ServerRoot  string            `json:"server_root,omitempty"`
	Databases   []string          `json:"databases,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Ports       map[string]string `json:"ports,omitempty"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

// LoadProjectConfig loads configuration for a specific project
func LoadProjectConfig(projectPath string) (*ProjectConfig, error) {
	configFile := filepath.Join(projectPath, ".devbox.json")

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil, nil // No project config yet
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var config ProjectConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse project config: %w", err)
	}

	return &config, nil
}

// SaveProjectConfig saves project configuration
func SaveProjectConfig(projectPath string, config *ProjectConfig) error {
	configFile := filepath.Join(projectPath, ".devbox.json")

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	return nil
}

// NetworkConfig holds network configuration
type NetworkConfig struct {
	BridgeName  string `json:"bridge_name"`
	Subnet      string `json:"subnet"`
	DNSServer   string `json:"dns_server"`
	InternalNet string `json:"internal_net"`
}

// GetNetworkConfig returns the default network configuration
func GetNetworkConfig() *NetworkConfig {
	return &NetworkConfig{
		BridgeName:  "devbox_devbox-network",
		Subnet:      "192.168.237.0/24",
		DNSServer:   "192.168.237.4",
		InternalNet: "devbox_internal",
	}
}
