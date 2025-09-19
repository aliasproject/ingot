package github

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/utils"
)

// Client handles GitHub authentication and API operations
type Client struct {
	config     *config.Config
	httpClient *http.Client
}

// NewClient creates a new GitHub client
func NewClient(cfg *config.Config) *Client {
	return &Client{
		config:     cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// EnsureAuthenticated ensures GitHub authentication is available
func (c *Client) EnsureAuthenticated() error {
	token := c.config.GetGithubToken()
	if token != "" {
		// Validate token
		if err := c.validateToken(token); err == nil {
			c.setupGHCR(token)
			return nil
		}
		// Token is invalid, clear it
		c.config.SetGithubToken("")
	}

	// Try to get token from environment
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		if err := c.validateToken(token); err == nil {
			c.config.SetGithubToken(token)
			c.setupGHCR(token)
			return nil
		}
	}

	// Try GitHub CLI
	if token, err := c.getTokenFromGitHubCLI(); err == nil && token != "" {
		if err := c.validateToken(token); err == nil {
			c.config.SetGithubToken(token)
			c.setupGHCR(token)
			return nil
		}
	}

	// No authentication available
	return fmt.Errorf("GitHub authentication not configured")
}

// Authenticate performs interactive GitHub authentication
func (c *Client) Authenticate() error {
	fmt.Println("🔑 GitHub Authentication Setup")
	fmt.Println(strings.Repeat("-", 50))

	// Check if already authenticated
	if token := c.config.GetGithubToken(); token != "" {
		if err := c.validateToken(token); err == nil {
			fmt.Println("✅ Already authenticated")
			username := c.getUsername(token)
			if username != "" {
				fmt.Printf("👤 Logged in as: %s\n", username)
			}
			return nil
		}
	}

	fmt.Println("\nChoose authentication method:")
	fmt.Println("1. GitHub Personal Access Token")
	fmt.Println("2. GitHub OAuth (Device Flow)")
	fmt.Println("3. GitHub CLI (gh)")
	fmt.Print("\nChoice (1-3): ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no input provided")
	}

	choice := strings.TrimSpace(scanner.Text())

	switch choice {
	case "1":
		return c.authenticateWithToken()
	case "2":
		return c.authenticateWithOAuth()
	case "3":
		return c.authenticateWithGitHubCLI()
	default:
		return fmt.Errorf("invalid choice")
	}
}

// authenticateWithToken handles personal access token authentication
func (c *Client) authenticateWithToken() error {
	fmt.Println("\n📝 Personal Access Token Authentication")
	fmt.Println("Create a token at: https://github.com/settings/tokens")
	fmt.Println("Required scopes: repo, read:packages, write:packages")
	fmt.Print("\nEnter token (or press Enter to cancel): ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no input provided")
	}

	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		fmt.Println("Authentication cancelled")
		return nil
	}

	// Validate token
	if err := c.validateToken(token); err != nil {
		return fmt.Errorf("invalid token: %w", err)
	}

	// Save token
	if err := c.config.SetGithubToken(token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	// Setup GHCR
	c.setupGHCR(token)

	username := c.getUsername(token)
	fmt.Printf("✅ Authenticated successfully as %s\n", username)
	return nil
}

// authenticateWithOAuth handles OAuth device flow authentication
func (c *Client) authenticateWithOAuth() error {
	fmt.Println("\n🔐 GitHub OAuth Authentication (Device Flow)")

	// Request device code
	deviceCode, err := c.requestDeviceCode()
	if err != nil {
		return fmt.Errorf("failed to request device code: %w", err)
	}

	fmt.Printf("\n🌐 Visit: %s\n", deviceCode.VerificationURI)
	fmt.Printf("📋 Enter code: %s\n", deviceCode.UserCode)
	fmt.Println("\nWaiting for authorization...")

	// Open browser
	utils.OpenURL(deviceCode.VerificationURI)

	// Poll for token
	token, err := c.pollForToken(deviceCode)
	if err != nil {
		return fmt.Errorf("failed to get token: %w", err)
	}

	// Save token
	if err := c.config.SetGithubToken(token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	// Setup GHCR
	c.setupGHCR(token)

	username := c.getUsername(token)
	fmt.Printf("✅ Authenticated successfully as %s\n", username)
	return nil
}

// authenticateWithGitHubCLI handles GitHub CLI authentication
func (c *Client) authenticateWithGitHubCLI() error {
	fmt.Println("\n🚀 GitHub CLI Authentication")

	// Check if gh is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GitHub CLI (gh) is not installed")
	}

	// Check if already authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		fmt.Println("GitHub CLI is not authenticated")
		fmt.Println("Run: gh auth login")
		return fmt.Errorf("GitHub CLI not authenticated")
	}

	// Get token
	token, err := c.getTokenFromGitHubCLI()
	if err != nil {
		return fmt.Errorf("failed to get token from GitHub CLI: %w", err)
	}

	// Validate token
	if err := c.validateToken(token); err != nil {
		return fmt.Errorf("invalid token from GitHub CLI: %w", err)
	}

	// Save token
	if err := c.config.SetGithubToken(token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	// Setup GHCR
	c.setupGHCR(token)

	username := c.getUsername(token)
	fmt.Printf("✅ Authenticated successfully as %s\n", username)
	return nil
}

// validateToken validates a GitHub token
func (c *Client) validateToken(token string) error {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid token (status: %d)", resp.StatusCode)
	}

	return nil
}

// getTokenFromGitHubCLI gets token from GitHub CLI
func (c *Client) getTokenFromGitHubCLI() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("no token returned from GitHub CLI")
	}

	return token, nil
}

// getUsername gets the GitHub username for a token
func (c *Client) getUsername(token string) string {
	// Try GitHub CLI first
	if _, err := exec.LookPath("gh"); err == nil {
		if cmd := exec.Command("gh", "api", "user", "--jq", ".login"); cmd != nil {
			if output, err := cmd.Output(); err == nil {
				if username := strings.TrimSpace(string(output)); username != "" {
					return username
				}
			}
		}
	}

	// Fall back to API call
	req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "user"
	}
	defer resp.Body.Close()

	var user struct {
		Login string `json:"login"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "user"
	}

	return user.Login
}

// setupGHCR configures Docker to use GitHub Container Registry
func (c *Client) setupGHCR(token string) {
	username := c.getUsername(token)
	if username == "" {
		username = "token"
	}

	cmd := exec.Command("docker", "login", "ghcr.io", "-u", username, "--password-stdin")
	cmd.Stdin = strings.NewReader(token)
	cmd.Run()
}

// DeviceCode represents the GitHub device code response
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// requestDeviceCode requests a device code for OAuth flow
func (c *Client) requestDeviceCode() (*DeviceCode, error) {
	body := strings.NewReader("client_id=" + config.ClientID + "&scope=repo,read:packages,write:packages")

	req, err := http.NewRequest("POST", "https://github.com/login/device/code", body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var deviceCode DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&deviceCode); err != nil {
		return nil, err
	}

	return &deviceCode, nil
}

// pollForToken polls for the access token after device authorization
func (c *Client) pollForToken(deviceCode *DeviceCode) (string, error) {
	interval := time.Duration(deviceCode.Interval) * time.Second
	timeout := time.After(time.Duration(deviceCode.ExpiresIn) * time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("authorization timeout")
		case <-ticker.C:
			token, err := c.checkDeviceAuthorization(deviceCode.DeviceCode)
			if err != nil {
				if strings.Contains(err.Error(), "authorization_pending") {
					continue // Keep polling
				}
				return "", err
			}
			if token != "" {
				return token, nil
			}
		}
	}
}

// checkDeviceAuthorization checks if the device has been authorized
func (c *Client) checkDeviceAuthorization(deviceCode string) (string, error) {
	body := strings.NewReader(fmt.Sprintf(
		"client_id=%s&device_code=%s&grant_type=urn:ietf:params:oauth:grant-type:device_code",
		config.ClientID, deviceCode,
	))

	req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error != "" {
		return "", fmt.Errorf(result.Error)
	}

	return result.AccessToken, nil
}
