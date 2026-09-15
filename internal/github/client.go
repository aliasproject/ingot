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

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/utils"
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
	fmt.Println(utils.Colorize("[AUTH]", utils.ColorBlue) + " GitHub Authentication Setup")
	fmt.Println(strings.Repeat("-", 50))

	// Check if already authenticated
	if token := c.config.GetGithubToken(); token != "" {
		if err := c.validateToken(token); err == nil {
			fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Already authenticated")
			username := c.getUsername(token)
			if username != "" {
				fmt.Printf(utils.Colorize("[USER]", utils.ColorCyan)+" Logged in as: %s\n", username)
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
	if err := c.setupGHCR(token); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: GitHub authentication succeeded but Docker login failed\n")
		fmt.Printf("   You may need to manually run: docker login ghcr.io\n")
		return err
	}

	username := c.getUsername(token)
	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Authenticated successfully as %s\n", username)
	return nil
}

// authenticateWithOAuth handles OAuth device flow authentication
func (c *Client) authenticateWithOAuth() error {
	fmt.Println("\n[AUTH] GitHub OAuth Authentication (Device Flow)")

	// Request device code
	deviceCode, err := c.requestDeviceCode()
	if err != nil {
		return fmt.Errorf("failed to request device code: %w", err)
	}

	fmt.Printf("\n[WEB] Visit: %s\n", deviceCode.VerificationURI)
	fmt.Printf(utils.Colorize("[CODE]", utils.ColorWhite)+" Enter code: %s\n", deviceCode.UserCode)
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
	if err := c.setupGHCR(token); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: GitHub authentication succeeded but Docker login failed\n")
		fmt.Printf("   You may need to manually run: docker login ghcr.io\n")
		return err
	}

	username := c.getUsername(token)
	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Authenticated successfully as %s\n", username)
	return nil
}

// authenticateWithGitHubCLI handles GitHub CLI authentication
func (c *Client) authenticateWithGitHubCLI() error {
	fmt.Println("\n[STARTING] GitHub CLI Authentication")

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
	if err := c.setupGHCR(token); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: GitHub authentication succeeded but Docker login failed\n")
		fmt.Printf("   You may need to manually run: docker login ghcr.io\n")
		return err
	}

	username := c.getUsername(token)
	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Authenticated successfully as %s\n", username)
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
func (c *Client) setupGHCR(token string) error {
	username := c.getUsername(token)
	if username == "" {
		username = "token"
	}

	fmt.Printf(utils.Colorize("[AUTH]", utils.ColorBlue) + " Logging into GitHub Container Registry (ghcr.io)...\n")

	cmd := exec.Command("docker", "login", "ghcr.io", "-u", username, "--password-stdin")
	cmd.Stdin = strings.NewReader(token)
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Show error output
		if len(output) > 0 {
			fmt.Printf("Docker login error: %s\n", string(output))
		}

		// Try to provide helpful context
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Docker login to ghcr.io failed: %v\n", err)
		fmt.Println("\n[INFO] Troubleshooting tips:")
		fmt.Println("   1. Ensure Docker is running: docker ps")
		fmt.Println("   2. Check Docker permissions: docker run hello-world")
		fmt.Println("   3. Verify your GitHub token has 'read:packages' scope")
		fmt.Println("   4. Try manual login: echo YOUR_TOKEN | docker login ghcr.io -u USERNAME --password-stdin")

		return fmt.Errorf("docker login failed: %w", err)
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Successfully logged into ghcr.io as %s\n", username)

	// A login succeeding says nothing about whether pulls will: GHCR
	// accepts any valid GitHub token at login but only serves private
	// packages to one with read:packages. Warn now rather than letting the
	// user find out via an opaque "denied" on the next image pull.
	if scopes, reported, err := c.TokenScopes(token); err == nil && reported && !HasPackagesScope(scopes) {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Token scopes (%s) do not include 'read:packages' - pulls from ghcr.io will be denied\n", strings.Join(scopes, ", "))
		fmt.Println("   Fix: gh auth refresh -h github.com -s read:packages   (or use a GITHUB_TOKEN that has read:packages)")
	}
	return nil
}

// FindToken returns the token EnsureAuthenticated would settle on and a
// human-readable description of where it came from, without validating it
// or touching docker login. Empty strings mean no token is available.
func (c *Client) FindToken() (token string, source string) {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, "GITHUB_TOKEN environment variable"
	}
	if token, err := c.getTokenFromGitHubCLI(); err == nil && token != "" {
		return token, "GitHub CLI (gh auth token)"
	}
	return "", ""
}

// ValidateToken checks a token against the GitHub API.
func (c *Client) ValidateToken(token string) error {
	return c.validateToken(token)
}

// Username returns the GitHub login for a token.
func (c *Client) Username(token string) string {
	return c.getUsername(token)
}

// TokenScopes returns the OAuth scopes GitHub reports for a token via the
// X-OAuth-Scopes response header. Classic personal access tokens and gh CLI
// OAuth tokens carry that header; fine-grained tokens do not, in which case
// reported is false and the caller can't tell what the token permits.
func (c *Client) TokenScopes(token string) (scopes []string, reported bool, err error) {
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("invalid token (status: %d)", resp.StatusCode)
	}

	header := resp.Header.Get("X-OAuth-Scopes")
	if header == "" {
		return nil, false, nil
	}
	for _, scope := range strings.Split(header, ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes, true, nil
}

// HasPackagesScope reports whether a scope list permits reading GHCR
// packages. write:packages and delete:packages imply read:packages.
func HasPackagesScope(scopes []string) bool {
	for _, scope := range scopes {
		switch scope {
		case "read:packages", "write:packages", "delete:packages":
			return true
		}
	}
	return false
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
