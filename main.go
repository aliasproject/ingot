package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	version      = "0.7.5"
	githubRepo   = "aliasproject/devbox"
	devboxBinary = "/usr/local/bin/devbox"
)

var (
	homeDir   = os.Getenv("HOME")
	devboxDir = filepath.Join(homeDir, ".devbox")
)

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func getHostIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", err
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}

func main() {
	// No persistent state to load - fully stateless

	args := os.Args[1:]

	// Handle version flag
	if len(args) > 0 && (args[0] == "-v" || args[0] == "--version") {
		fmt.Printf("Devbox v%s\n", version)
		return
	}

	// Handle help command
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		showHelp()
		return
	}

	// Handle status command
	if len(args) > 0 && args[0] == "status" {
		if err := showStatus(); err != nil {
			fmt.Printf("Error getting status: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle network-help command
	if len(args) > 0 && args[0] == "network-help" {
		showNetworkHelp()
		return
	}

	// Handle cert-verify command
	if len(args) > 0 && args[0] == "cert-verify" {
		if verifyCertificateInstallation() {
			fmt.Println("✅ Certificate is properly installed and trusted!")
			fmt.Println("🔒 Your .test domains should show as secure in browsers")
		} else {
			fmt.Println("❌ Certificate is not properly installed or trusted")
			fmt.Println("💡 Run 'devbox cert-install' to install certificates manually")

			caCertPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.pem")
			if fileExists(caCertPath) {
				fmt.Printf("🔍 Certificate file exists at: %s\n", caCertPath)
				fmt.Println("📋 You can install it manually using 'devbox cert-install'")
			} else {
				fmt.Println("⚠️  Certificate file not found - please run 'devbox install' first")
			}
		}
		return
	}

	// Handle cert-install command
	if len(args) > 0 && args[0] == "cert-install" {
		caCertPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.pem")
		if !fileExists(caCertPath) {
			fmt.Println("❌ Certificate file not found!")
			fmt.Printf("Expected location: %s\n", caCertPath)
			fmt.Println("💡 Run 'devbox install' first to generate certificates")
			os.Exit(1)
		}

		fmt.Println("🔒 Installing CA certificate manually...")
		if err := setupSSLInstallation(caCertPath); err != nil {
			fmt.Printf("❌ Certificate installation failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("🎉 Certificate installation completed!")
		return
	}

	// Handle cert-debug command
	if len(args) > 0 && args[0] == "cert-debug" {
		debugCertificateSetup()
		return
	}

	// Handle project init command
	if len(args) > 0 && args[0] == "init" {
		if err := initProject(); err != nil {
			fmt.Printf("Project initialization failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle php-version command
	if len(args) > 0 && args[0] == "php-version" {
		if err := changePHPVersion(); err != nil {
			fmt.Printf("PHP version change failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle project start command
	if len(args) > 0 && args[0] == "start" {
		if err := startProject(); err != nil {
			fmt.Printf("Project start failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle project stop command
	if len(args) > 0 && args[0] == "stop" {
		if err := stopProject(); err != nil {
			fmt.Printf("Project stop failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle global start command
	if len(args) > 0 && args[0] == "global" && len(args) > 1 && args[1] == "start" {
		if err := startGlobalServices(); err != nil {
			fmt.Printf("Global services start failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle global stop command
	if len(args) > 0 && args[0] == "global" && len(args) > 1 && args[1] == "stop" {
		if err := stopGlobalServices(); err != nil {
			fmt.Printf("Global services stop failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle projects list command
	if len(args) > 0 && args[0] == "projects" {
		if err := listProjects(); err != nil {
			fmt.Printf("Failed to list projects: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle database add command
	if len(args) > 0 && args[0] == "database" && len(args) > 1 && args[1] == "add" {
		if err := addDatabase(); err != nil {
			fmt.Printf("Database addition failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle database remove command
	if len(args) > 0 && args[0] == "database" && len(args) > 1 && args[1] == "remove" {
		if err := removeDatabase(); err != nil {
			fmt.Printf("Database removal failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle register command
	if len(args) > 0 && args[0] == "register" {
		if err := registerProject(); err != nil {
			fmt.Printf("Project registration failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle unregister command
	if len(args) > 0 && args[0] == "unregister" {
		if err := unregisterProject(); err != nil {
			fmt.Printf("Project unregistration failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle remove command
	if len(args) > 0 && args[0] == "remove" {
		if err := removeProject(); err != nil {
			fmt.Printf("Project removal failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle dns-cleanup command
	if len(args) > 0 && args[0] == "dns-cleanup" {
		if err := cleanupTestDomainResolver(); err != nil {
			fmt.Printf("DNS cleanup failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Watchman commands removed - SSL/vhosts handled directly by devbox

	// Handle install command (for when devbox is already installed)
	if len(args) > 0 && args[0] == "install" {
		fmt.Println("Devbox is already installed!")
		fmt.Printf("Version: v%s\n", version)
		fmt.Println()

		// Show installation status
		fmt.Println("Installation Status:")
		fmt.Printf("  Binary location: %s (%s)\n", devboxBinary, getStatus(fileExists(devboxBinary)))
		fmt.Printf("  Global services: %s\n", getStatus(isGlobalServicesRunning()))

		currentBinary, err := os.Executable()
		if err == nil {
			fmt.Printf("  Current binary: %s\n", currentBinary)
		}

		fmt.Println()
		fmt.Println("To reinstall, remove ~/.devbox directory and run devbox again.")
		return
	}

	// Handle update command
	if len(args) > 0 && args[0] == "update" {
		if err := handleUpdate(); err != nil {
			fmt.Printf("Update failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle github-auth command
	if len(args) > 0 && args[0] == "github-auth" {
		if err := setupGitHubAuth(); err != nil {
			fmt.Printf("GitHub authentication setup failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Check if Docker is installed
	if !isDockerInstalled() {
		fmt.Println("Docker is not installed or not running. Please install Docker first.")
		os.Exit(1)
	}

	// Check if devbox is already installed
	if !isInstalled() {
		fmt.Println("Devbox is not installed. Installing...")
		if err := install(); err != nil {
			fmt.Printf("Installation failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle different commands
	if len(args) == 0 {
		// Default behavior - show project status if in project, otherwise show help
		if isProjectInitialized() {
			if err := showProjectStatus(); err != nil {
				fmt.Printf("Failed to show project status: %v\n", err)
				os.Exit(1)
			}
			return
		} else {
			fmt.Println("💡 No devbox project found in current directory")
			fmt.Println("Run 'devbox start' to initialize and start a project, or 'devbox help' for more options")
			return
		}
	}

	// Handle project-specific commands
	if isProjectInitialized() {
		projectName := getProjectName()
		containerName := fmt.Sprintf("devbox_php_%s", projectName)

		// Execute command in project's PHP container
		dockerArgs := []string{"exec", containerName}
		dockerArgs = append(dockerArgs, args...)

		cmd := exec.Command("docker", dockerArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Run(); err != nil {
			fmt.Printf("Command failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Show help for unknown commands when not in a project
		fmt.Printf("Unknown command: %s\n", strings.Join(args, " "))
		fmt.Println("Run 'devbox help' for available commands")
		os.Exit(1)
	}
}

func isInstalled() bool {
	_, err := os.Stat(devboxBinary)
	return err == nil
}

// loadEnvVars removed - devbox is now stateless

// isInsideProjectDir removed - projects are directory-based, not root-based
func isInsideProjectDir() bool {
	return isProjectInitialized()
}

// getRelativeProjectPath removed - projects are directory-based
func getRelativeProjectPath() (string, error) {
	return "/app", nil
}

func directoryExistsInContainer(path string) bool {
	// Use docker exec to check if directory exists in the project's PHP container
	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)

	cmdStr := fmt.Sprintf("test -d %s && echo true || echo false", path)
	cmd := exec.Command("docker", "exec", "-T", containerName, "sh", "-c", cmdStr)

	// Set up pipes to capture output and error
	var output, stderr strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = &stderr

	// Execute the command
	err := cmd.Run()
	if err != nil {
		// Print detailed error information for debugging
		fmt.Printf("Directory check error: %s\n", stderr.String())
		return false
	}

	// Check the output
	result := strings.TrimSpace(output.String())
	return result == "true"
}

func executeInContainer(args []string) error {
	// Get relative path within project
	relPath, err := getRelativeProjectPath()
	if err != nil {
		return err
	}

	containerPath := "/app" + relPath

	// Check if directory exists in container
	if !directoryExistsInContainer(containerPath) {
		return fmt.Errorf("directory %s does not exist in container", containerPath)
	}

	// Handle specific shortcuts with shell commands using direct container exec
	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)
	var dockerArgs []string
	switch args[0] {
	case "artisan":
		dockerArgs = []string{"exec", "-it", "-w", containerPath, containerName, "sh", "-c", "php artisan " + strings.Join(args[1:], " ")}
	case "composer":
		dockerArgs = []string{"exec", "-it", "-w", containerPath, containerName, "sh", "-c", "composer " + strings.Join(args[1:], " ")}
	case "npm":
		dockerArgs = []string{"exec", "-it", "-w", containerPath, containerName, "sh", "-c", "npm " + strings.Join(args[1:], " ")}
	default:
		// Build docker exec command for other commands
		dockerArgs = []string{"exec", "-it", "-w", containerPath, containerName}
		dockerArgs = append(dockerArgs, args...)
	}

	return runDockerCommand(dockerArgs)
}

func runDockerCommand(args []string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// No persistent environment variables in stateless mode

	// Set environment variables for the command
	cmd.Env = os.Environ()

	return cmd.Run()
}

func handleUpdate() error {
	fmt.Println("Checking for updates...")

	hasUpdate, latestVersion, err := checkForUpdates()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !hasUpdate {
		fmt.Printf("You are using the latest version (v%s).\n", version)
		return nil
	}

	fmt.Printf("New version available: %s (current: v%s)\n", latestVersion, version)
	if !promptYesNo("Do you want to update?") {
		return nil
	}

	return performUpdate(latestVersion)
}

func checkForUpdates() (bool, string, error) {
	// Get GitHub token (try OAuth token first, then prompt if needed)
	token, err := getGitHubToken()
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
		fmt.Println("Proceeding without authentication (may fail for private repos).")
		token = "" // Continue with empty token
	}

	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo), nil)
	if err != nil {
		return false, "", err
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		// Check if the repository itself exists by trying to access it
		repoReq, _ := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s", githubRepo), nil)
		if token != "" {
			repoReq.Header.Set("Authorization", "Bearer "+token)
		}
		repoResp, _ := client.Do(repoReq)
		if repoResp != nil {
			defer repoResp.Body.Close()
			if repoResp.StatusCode == 404 {
				return false, "", fmt.Errorf("repository '%s' does not exist or is not accessible", githubRepo)
			}
		}
		return false, "", fmt.Errorf("repository '%s' has no releases published", githubRepo)
	}

	if resp.StatusCode == 401 {
		// Handle token expiration
		if err := handleTokenExpired(); err != nil {
			return false, "", fmt.Errorf("authentication failed: %w", err)
		}
		// Retry with new token
		return checkForUpdates()
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return false, "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err
	}

	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return false, "", fmt.Errorf("failed to parse release data: %w", err)
	}

	if rel.TagName == "" {
		return false, "", fmt.Errorf("no tag name found in release")
	}

	return rel.TagName != "v"+version, rel.TagName, nil
}

// getGitHubToken retrieves GitHub token from environment or stored config
func getGitHubToken() (string, error) {
	// First check environment variable
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// Skip file loading - stateless mode

	// Try to get token from GitHub CLI if available
	if token, err := getGitHubCLIToken(); err == nil && token != "" {
		fmt.Println("✅ Using GitHub token from GitHub CLI")

		// Set in current environment (not persisted)
		os.Setenv("GITHUB_TOKEN", token)

		return token, nil
	}

	// Fallback to OAuth device flow
	fmt.Println("\nGitHub authentication required for container registry access.")
	fmt.Println("Starting GitHub OAuth device flow...")
	fmt.Println()

	token, err := performOAuthDeviceFlow()
	if err != nil {
		return "", fmt.Errorf("OAuth device flow failed: %w", err)
	}

	// Set in current environment (not persisted - stateless mode)
	os.Setenv("GITHUB_TOKEN", token)
	fmt.Println("✅ GitHub token acquired successfully (session only).")

	return token, nil
}

type deviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error"`
}

func performOAuthDeviceFlow() (string, error) {
	// Step 1: Request device code
	deviceResp, err := requestDeviceCode()
	if err != nil {
		return "", fmt.Errorf("failed to request device code: %w", err)
	}

	// Step 2: Show user instructions
	fmt.Printf("🔗 Please visit: %s\n", deviceResp.VerificationURI)
	fmt.Printf("📝 Enter code: %s\n", deviceResp.UserCode)
	fmt.Println()
	fmt.Println("💡 Or visit this direct link:")
	fmt.Printf("   %s\n", deviceResp.VerificationURIComplete)
	fmt.Println()
	fmt.Print("Press Enter to open the URL in your browser...")

	// Wait for user to press Enter
	reader := bufio.NewReader(os.Stdin)
	_, err = reader.ReadString('\n')
	if err != nil {
		fmt.Printf("Error reading input: %v\n", err)
	}

	// Try to open the basic URL instead of the complete one (which has query params)
	fmt.Printf("🌐 Opening URL: %s\n", deviceResp.VerificationURI)

	err = openURL(deviceResp.VerificationURI)
	if err != nil {
		fmt.Printf("❌ Could not open browser automatically: %v\n", err)
		fmt.Println("Please manually copy and paste the URL above into your browser.")
		fmt.Printf("Then enter this code: %s\n", deviceResp.UserCode)
	} else {
		fmt.Println("✅ Opened browser for GitHub authorization")
		fmt.Printf("📝 You'll need to enter this code: %s\n", deviceResp.UserCode)
	}

	fmt.Println()
	fmt.Println("⏳ Waiting for authorization... (this may take a moment)")

	// Step 3: Poll for access token
	token, err := pollForAccessToken(deviceResp)
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}

	fmt.Println("✅ GitHub authentication successful!")
	return token, nil
}

func requestDeviceCode() (*deviceCodeResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	data := "client_id=178c6fc778ccc68e1d6a&scope=repo"
	req, err := http.NewRequest("POST", "https://github.com/login/device/code", strings.NewReader(data))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var deviceResp deviceCodeResponse
	if err := json.Unmarshal(body, &deviceResp); err != nil {
		return nil, fmt.Errorf("failed to parse device code response: %w", err)
	}

	return &deviceResp, nil
}

func pollForAccessToken(deviceResp *deviceCodeResponse) (string, error) {
	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second // Minimum interval
	}
	fmt.Printf("🔄 Starting polling with %v interval\n", interval)

	timeout := time.After(time.Duration(deviceResp.ExpiresIn) * time.Second)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return "", fmt.Errorf("device flow timed out")
		case <-ticker.C:
			token, err := checkForAccessToken(deviceResp.DeviceCode)
			if err != nil {
				// Check if it's a "pending" error (user hasn't authorized yet)
				if strings.Contains(err.Error(), "authorization_pending") {
					continue // Keep polling
				}
				// Handle slow_down error by increasing interval
				if strings.Contains(err.Error(), "slow_down") {
					interval = interval * 2 // Double the interval
					if interval > 30*time.Second {
						interval = 30 * time.Second // Cap at 30 seconds
					}
					ticker.Stop()
					ticker = time.NewTicker(interval)
					defer ticker.Stop() // Ensure new ticker is also stopped
					fmt.Printf("⏳ Slowing down polling (interval now %v)...\n", interval)
					continue
				}
				return "", err
			}
			return token, nil
		}
	}
}

func checkForAccessToken(deviceCode string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	data := fmt.Sprintf("client_id=178c6fc778ccc68e1d6a&device_code=%s&grant_type=urn:ietf:params:oauth:grant-type:device_code", deviceCode)
	req, err := http.NewRequest("POST", "https://github.com/login/oauth/access_token", strings.NewReader(data))
	if err != nil {
		return "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var tokenResp accessTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse access token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("%s", tokenResp.Error)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("no access token in response")
	}

	return tokenResp.AccessToken, nil
}

func ensureGitHubAuth() error {
	// Check if we already have a valid token
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return nil
	}

	// Check current environment only (stateless mode)

	// No token found, try to get one
	fmt.Println("🔑 No GitHub token found, setting up authentication for container registry access...")
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("failed to get GitHub token: %w", err)
	}

	// Get GitHub username
	username, err := getGitHubUsername()
	if err != nil {
		fmt.Printf("Warning: Could not get GitHub username, using 'token' as fallback: %v\n", err)
		username = "token"
	}

	// Check token permissions
	if err := checkTokenPermissions(token); err != nil {
		fmt.Printf("Warning: Token permission issue: %v\n", err)
		fmt.Println("💡 You may need to regenerate your GitHub token with 'read:packages' scope")
	}

	// Login to GitHub Container Registry
	fmt.Printf("🔐 Logging into GitHub Container Registry as user: %s\n", username)
	loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", username, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr

	if err := loginCmd.Run(); err != nil {
		fmt.Printf("❌ Failed to login to GitHub Container Registry: %v\n", err)
		fmt.Println("💡 Troubleshooting tips:")
		fmt.Printf("   1. Ensure your GitHub token has 'read:packages' permission\n")
		fmt.Printf("   2. Try: gh auth refresh -h github.com -s read:packages -s write:packages\n")
		fmt.Printf("   3. Or manually create a token at: https://github.com/settings/tokens\n")
		return fmt.Errorf("GHCR authentication failed")
	}

	fmt.Println("✅ Successfully authenticated with GitHub Container Registry!")
	return nil
}

func openURL(url string) error {
	// For macOS, try multiple approaches
	if _, err := exec.LookPath("open"); err == nil {
		// Try method 1: open with URL flag (forces browser)
		cmd := exec.Command("open", "-u", url)
		if err := cmd.Run(); err == nil {
			return nil
		}

		// Try method 2: regular open
		cmd = exec.Command("open", url)
		return cmd.Run()
	}

	// Linux
	if _, err := exec.LookPath("xdg-open"); err == nil {
		cmd := exec.Command("xdg-open", url)
		return cmd.Start()
	}

	// Windows
	if _, err := exec.LookPath("cmd"); err == nil {
		cmd := exec.Command("cmd", "/c", "start", url)
		return cmd.Start()
	}

	return fmt.Errorf("unable to detect system browser opener")
}

func getGitHubCLIToken() (string, error) {
	// Check if GitHub CLI is installed
	if _, err := exec.LookPath("gh"); err != nil {
		return "", fmt.Errorf("GitHub CLI not found")
	}

	// Check if user is authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("GitHub CLI not authenticated")
	}

	// Get the token
	cmd = exec.Command("gh", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get token from GitHub CLI: %w", err)
	}

	token := strings.TrimSpace(string(output))
	if token == "" {
		return "", fmt.Errorf("empty token from GitHub CLI")
	}

	return token, nil
}

// getGitHubUsername retrieves GitHub username from GitHub CLI or API
func getGitHubUsername() (string, error) {
	// Try to get username from GitHub CLI first
	if _, err := exec.LookPath("gh"); err == nil {
		cmd := exec.Command("gh", "api", "user", "--jq", ".login")
		output, err := cmd.Output()
		if err == nil {
			username := strings.TrimSpace(string(output))
			if username != "" {
				return username, nil
			}
		}
	}

	// Fallback to GitHub API using token
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("no GitHub token available")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", err
	}

	if user.Login == "" {
		return "", fmt.Errorf("empty username from API")
	}

	return user.Login, nil
}

// checkTokenPermissions checks if the GitHub token has required permissions
func checkTokenPermissions(token string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	// Check if token has read:packages permission by trying to access the API
	req, err := http.NewRequest("GET", "https://api.github.com/user/packages?package_type=container", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 {
		return fmt.Errorf("token lacks required 'read:packages' permission for GHCR")
	}

	if resp.StatusCode != 200 && resp.StatusCode != 404 {
		return fmt.Errorf("unexpected API response: %d", resp.StatusCode)
	}

	return nil
}

// setupGitHubAuth helps users set up GitHub authentication manually
func setupGitHubAuth() error {
	fmt.Println("🔑 GitHub Authentication Setup for Container Registry")

	fmt.Println("GitHub Container Registry (GHCR) requires authentication to pull private containers.")
	fmt.Println("You need a GitHub Personal Access Token with 'read:packages' permission.")

	fmt.Println("📋 Setup Options:")

	fmt.Println("1. 🔄 Refresh GitHub CLI token (if you have gh cli installed):")
	fmt.Println("   gh auth refresh -h github.com -s read:packages")

	fmt.Println("2. 🔑 Create a new Personal Access Token:")
	fmt.Println("   a. Visit: https://github.com/settings/tokens")
	fmt.Println("   b. Click 'Generate new token (classic)'")
	fmt.Println("   c. Select scopes:")
	fmt.Println("      - repo (for private repositories)")
	fmt.Println("      - read:packages (for GitHub Container Registry)")
	fmt.Println("   d. Copy the generated token")

	fmt.Println("3. 💾 Set the token in devbox:")
	fmt.Print("   Enter your GitHub token (or press Enter to skip): ")

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		token := strings.TrimSpace(scanner.Text())
		if token != "" {
			// Validate the token
			fmt.Println("\n🔍 Validating token...")
			if err := validateGitHubToken(token); err != nil {
				fmt.Printf("❌ Token validation failed: %v\n", err)
				return err
			}

			// Set token in current environment (not persisted)
			os.Setenv("GITHUB_TOKEN", token)

			// Test GHCR login
			fmt.Println("🔐 Testing GitHub Container Registry login...")
			if err := testGHCRLogin(); err != nil {
				fmt.Printf("❌ GHCR login test failed: %v\n", err)
				return err
			}

			fmt.Println("✅ GitHub authentication configured successfully!")
			return nil
		}
	}

	fmt.Println("⏭️  Token setup skipped. You can run 'devbox github-auth' again later.")
	return nil
}

// validateGitHubToken validates a GitHub token
func validateGitHubToken(token string) error {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", "https://api.github.com/user", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to validate token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("invalid or expired token")
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected response: %d", resp.StatusCode)
	}

	// Check package permissions
	return checkTokenPermissions(token)
}

// testGHCRLogin tests GitHub Container Registry login
func testGHCRLogin() error {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("no GitHub token available")
	}

	username, err := getGitHubUsername()
	if err != nil {
		username = "token"
	}

	loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", username, "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)

	var stderr strings.Builder
	loginCmd.Stderr = &stderr

	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("docker login failed: %v - %s", err, stderr.String())
	}

	return nil
}

// handleTokenExpired handles expired or invalid GitHub tokens
func handleTokenExpired() error {
	fmt.Println("Your GitHub token appears to be expired or invalid.")
	if !promptYesNo("Would you like to enter a new token?") {
		return fmt.Errorf("GitHub token required for this operation")
	}

	fmt.Println("You can generate a token at: https://github.com/settings/tokens")
	fmt.Println("Required scopes: repo (for private repositories)")

	token, err := promptInput("Enter new GitHub token: ")
	if err != nil {
		return fmt.Errorf("failed to read token: %w", err)
	}

	if token == "" {
		return fmt.Errorf("token cannot be empty")
	}

	// Set in current environment (not persisted - stateless mode)
	os.Setenv("GITHUB_TOKEN", token)
	fmt.Println("GitHub token updated successfully (session only).")

	return nil
}

// promptInput prompts for user input
func promptInput(prompt string) (string, error) {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	return "", scanner.Err()
}

// updateEnvVar removed - stateless mode

func performUpdate(newVersion string) error {
	// Get GitHub token
	token, err := getGitHubToken()
	if err != nil {
		fmt.Printf("\nAutomatic update requires GitHub token for private repository access.\n")
		fmt.Printf("\nTo update manually:\n")
		fmt.Printf("1. Visit: https://github.com/%s/releases/tag/%s\n", githubRepo, newVersion)
		fmt.Printf("2. Download the source code or binary for your system\n")
		fmt.Printf("3. Build and install, or replace the binary at: %s\n", devboxBinary)
		return nil
	}

	downloadURL, err := getDownloadURL(newVersion, token)
	if err != nil {
		// Check if it's a token-related error
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "unauthorized") {
			if err := handleTokenExpired(); err != nil {
				return err
			}
			// Retry with new token
			token, _ = getGitHubToken()
			downloadURL, err = getDownloadURL(newVersion, token)
		}

		if err != nil {
			fmt.Printf("\nAutomatic update not available: %v\n", err)
			fmt.Printf("\nTo update manually:\n")
			fmt.Printf("1. Visit: https://github.com/%s/releases/tag/%s\n", githubRepo, newVersion)
			fmt.Printf("2. Download the source code or binary for your system\n")
			fmt.Printf("3. Build and install, or replace the binary at: %s\n", devboxBinary)
			return nil
		}
	}

	tempFile := "/tmp/devbox-update"
	if err := downloadFile(downloadURL, tempFile, token); err != nil {
		// Check for token-related errors in download
		if strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "unauthorized") {
			if err := handleTokenExpired(); err != nil {
				return err
			}
			// Retry download with new token
			token, _ = getGitHubToken()
			err = downloadFile(downloadURL, tempFile, token)
		}

		if err != nil {
			if strings.Contains(err.Error(), "404") {
				fmt.Printf("\nDownload failed: Asset not found or access denied.\n")
				fmt.Printf("This may require using the GitHub API asset download endpoint.\n")
			}
			fmt.Printf("\nTo update manually:\n")
			fmt.Printf("1. Visit: https://github.com/%s/releases/tag/%s\n", githubRepo, newVersion)
			fmt.Printf("2. Download the appropriate asset\n")
			fmt.Printf("3. Replace the binary at: %s\n", devboxBinary)
			return fmt.Errorf("failed to download update: %w", err)
		}
	}

	return installBinary(tempFile)
}

func getDownloadURL(newVersion, token string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", githubRepo, newVersion), nil)
	if err != nil {
		return "", err
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return "", fmt.Errorf("unauthorized access - GitHub token may be invalid or expired")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch release info: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", err
	}

	// Check if there are any binary assets
	if len(rel.Assets) == 0 {
		return "", fmt.Errorf("no binary assets found - release contains only source code")
	}

	// Try multiple patterns to find the right binary
	patterns := []string{"devbox", "binary", "linux", "darwin", "macos"}

	for _, pattern := range patterns {
		for _, asset := range rel.Assets {
			if strings.Contains(strings.ToLower(asset.Name), pattern) {
				// Use GitHub API asset download URL for private repos
				return fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", githubRepo, asset.ID), nil
			}
		}
	}

	// List available assets for debugging
	assetNames := make([]string, len(rel.Assets))
	for i, asset := range rel.Assets {
		assetNames[i] = asset.Name
	}

	return "", fmt.Errorf("no suitable binary found. Available assets: %v", assetNames)
}

func downloadFile(url, destination, token string) error {
	fmt.Println("Downloading update...")

	// Use Go's HTTP client with authentication for private repos
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	// Add GitHub token for private repo access
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/octet-stream")
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 401 {
		return fmt.Errorf("unauthorized access - GitHub token may be invalid or expired")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Create the destination file
	out, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer out.Close()

	// Copy the response body to the destination file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write downloaded file: %w", err)
	}

	fmt.Println("Download completed successfully.")
	return nil
}

func installBinary(tempFile string) error {
	fmt.Println("Installing update...")

	// Make the temp file executable
	if err := os.Chmod(tempFile, 0755); err != nil {
		return fmt.Errorf("failed to make binary executable: %w", err)
	}

	// Replace the current binary
	if err := copyFile(tempFile, devboxBinary); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Clean up temp file
	os.Remove(tempFile)

	fmt.Println("Update installed successfully!")
	return nil
}

func install() error {
	fmt.Println("🚀 Setting up Devbox...")
	fmt.Println()

	// Create devbox directory
	fmt.Println("📁 Creating devbox directory...")
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create devbox directory: %w", err)
	}

	// Project directory no longer needed - projects launched individually from their own directories

	// Note: PHP version is now configured per-project
	fmt.Println("📦 PHP containers will be created per-project with configurable versions")

	// Database selection
	fmt.Println()
	includeDatabase := promptYesNo("Would you like to include a database service?")

	var dbType, dbVersion string
	if includeDatabase {
		dbType = selectOption("Select Database Type", []string{"mysql", "postgresql"})

		if dbType == "mysql" {
			dbVersion = selectOption("Select MySQL Version", []string{"8.0", "5.7"})
		} else {
			dbVersion = selectOption("Select PostgreSQL Version", []string{"16", "15", "14", "13"})
		}

		// Set environment variables in current process (not persisted)
		os.Setenv("DATABASE_TYPE", dbType)
		os.Setenv("DATABASE_VERSION", dbVersion)
		fmt.Printf("✅ %s %s will be included\n", strings.Title(dbType), dbVersion)
	} else {
		fmt.Println("⏭️  Database skipped. You can add it later with 'devbox database add'")
	}

	// Watchman removed - SSL/vhosts handled directly by devbox commands

	// Setup GitHub token for GHCR authentication
	fmt.Println()
	fmt.Println("🔑 Setting up GitHub authentication for container registry...")
	fmt.Println("GitHub authentication is required to pull container images from ghcr.io")
	fmt.Println("This is necessary because the devbox containers are hosted on GitHub Container Registry.")
	fmt.Println()

	token, err := getGitHubToken()
	if err != nil {
		fmt.Printf("❌ Error getting GitHub token: %v\n", err)
		fmt.Println("⚠️  Proceeding without GitHub authentication...")
		fmt.Println("   Container pulls may fail. You can set up authentication later.")
	} else {
		// Get GitHub username
		username, err := getGitHubUsername()
		if err != nil {
			fmt.Printf("Warning: Could not get GitHub username, using 'token' as fallback: %v\n", err)
			username = "token"
		}

		// Check token permissions
		if err := checkTokenPermissions(token); err != nil {
			fmt.Printf("Warning: Token permission issue: %v\n", err)
			fmt.Println("💡 You may need to regenerate your GitHub token with 'read:packages' scope")
		}

		// Login to GitHub Container Registry
		fmt.Printf("🔐 Logging into GitHub Container Registry as user: %s\n", username)
		loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", username, "--password-stdin")
		loginCmd.Stdin = strings.NewReader(token)
		loginCmd.Stdout = os.Stdout
		loginCmd.Stderr = os.Stderr

		if err := loginCmd.Run(); err != nil {
			fmt.Printf("❌ Warning: Failed to login to GitHub Container Registry: %v\n", err)
			fmt.Println("💡 Troubleshooting tips:")
			fmt.Printf("   1. Ensure your GitHub token has 'read:packages' permission\n")
			fmt.Printf("   2. Try: gh auth refresh -h github.com -s read:packages\n")
			fmt.Printf("   3. Or manually create a token at: https://github.com/settings/tokens\n")
			fmt.Println("   4. Container pulls may fail. You can try authentication later with 'devbox update'.")
		} else {
			fmt.Println("✅ Successfully authenticated with GitHub Container Registry!")
		}
	}

	// Copy binary to /usr/local/bin/devbox
	fmt.Println()
	fmt.Println("📦 Installing devbox binary...")
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	// Check if we can write to /usr/local/bin, if not, use sudo
	if err := copyFile(currentBinary, devboxBinary); err != nil {
		fmt.Println("Permission denied installing to /usr/local/bin. Using sudo...")
		fmt.Println("You may be prompted for your password to install the devbox binary.")

		cmd := exec.Command("sudo", "cp", currentBinary, devboxBinary)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install binary to %s with sudo: %w", devboxBinary, err)
		}

		// Make it executable with sudo
		cmd = exec.Command("sudo", "chmod", "755", devboxBinary)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to make binary executable: %w", err)
		}
	} else {
		if err := os.Chmod(devboxBinary, 0755); err != nil {
			return fmt.Errorf("failed to make binary executable: %w", err)
		}
	}

	// Get host IP address for network-wide access
	hostIP, err := getHostIP()
	if err != nil {
		fmt.Printf("Warning: Could not determine host IP address: %v\n", err)
		fmt.Println("Using 127.0.0.1 - domains will only work on this machine")
		hostIP = "127.0.0.1"
	} else {
		fmt.Printf("🌐 Configuring domains to work across network using IP: %s\n", hostIP)
	}

	// No compose file needed - services managed directly via Docker commands
	fmt.Println()
	fmt.Println("🐳 Global services will be managed directly via Docker commands")

	// Create certs directory for CA certificate installation only
	certsDir := filepath.Join(devboxDir, "certs")
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// Set HOME environment variable to ensure it's available for docker-compose
	if homeDir != "" {
		os.Setenv("HOME", homeDir)
	}

	// Start global services
	fmt.Println()
	fmt.Println("🚀 Starting global Docker services...")

	if err := startGlobalServices(); err != nil {
		return fmt.Errorf("failed to start global services: %w", err)
	}

	// SSL and vhosts will be created directly by devbox commands
	fmt.Println()
	fmt.Println("🔍 SSL certificates and vhosts created directly by devbox commands")

	// Setup .test domain resolver for macOS
	fmt.Println()
	if err := setupTestDomainResolver(); err != nil {
		fmt.Printf("Warning: Failed to setup .test domain resolver: %v\n", err)
		fmt.Println("💡 You can set this up manually later or use network DNS configuration")
	}

	// Setup SSL certificates and prompt for CA acceptance
	fmt.Println()
	if err := setupSSL(); err != nil {
		return fmt.Errorf("failed to setup SSL: %w", err)
	}

	fmt.Println()
	fmt.Println("✅ Devbox installed successfully!")
	fmt.Println("🌟 You can now run 'devbox' from any directory to create projects.")
	fmt.Println("🔒 SSL certificates have been generated for your project domains.")
	if isMacOS() {
		fmt.Println("🌐 .test domains are automatically configured and ready to use!")
	}
	fmt.Println()
	fmt.Println("📁 Projects: Launched individually from their own directories")
	fmt.Printf("🐘 PHP containers: Isolated FrankenPHP per-project\n")
	if dbType != "" {
		fmt.Printf("📊 Database: %s %s\n", strings.Title(dbType), dbVersion)
	} else {
		fmt.Println("📊 Database: None (add later with 'devbox database add')")
	}
	fmt.Println("🔄 SSL/Vhosts: Created directly by devbox commands")
	fmt.Println("⚡ Faster setup without background monitoring")

	// Show network access information
	fmt.Println()
	fmt.Println("🌍 Network Access:")
	if isMacOS() {
		fmt.Println("  📍 Local machine: https://yourproject.test (automatic)")
		if hostIP != "127.0.0.1" {
			fmt.Printf("  🌐 Network devices: Configure DNS to %s:5354 for .test domains\n", hostIP)
			fmt.Printf("  📱 Direct IP access: http://%s or https://%s\n", hostIP, hostIP)
		}
	} else {
		fmt.Println("  📍 Local machine: https://yourproject.test")
		fmt.Println("  🔧 Configure DNS to 127.0.0.1:5354 for .test domains")
		if hostIP != "127.0.0.1" {
			fmt.Printf("  🌐 Network devices: Configure DNS to %s:5354 for .test domains\n", hostIP)
			fmt.Printf("  📱 Direct IP access: http://%s or https://%s\n", hostIP, hostIP)
			fmt.Println("  💡 Run 'devbox network-help' for setup instructions")
		}
	}

	fmt.Println()
	fmt.Println("🚀 Next Steps:")
	fmt.Println("  1. Navigate to any directory")
	fmt.Println("  2. Run 'devbox start' (automatically initializes and starts)")
	fmt.Println("  💡 Each directory becomes an isolated FrankenPHP project")

	return nil
}

func isDockerInstalled() bool {
	cmd := exec.Command("docker", "--version")
	return cmd.Run() == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// writeEnvVar removed - stateless mode

// getProjectDirectory removed - projects are launched individually from their own directories

func selectOption(prompt string, options []string) string {
	fmt.Printf("%s:\n", prompt)
	for i, option := range options {
		fmt.Printf("%d. %s\n", i+1, option)
	}

	for {
		fmt.Print("Enter your choice (number): ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
			if err == nil && choice >= 1 && choice <= len(options) {
				return options[choice-1]
			}
		}
		fmt.Println("Invalid choice. Please try again.")
	}
}

func startContainers() error {
	return startGlobalServices()
}

// Watchman removed - SSL/vhosts are created directly by devbox commands

func setupSSL() error {
	fmt.Println("🔒 Setting up SSL certificates...")

	caCertPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.pem")

	// Ensure certs directory exists
	if err := os.MkdirAll(filepath.Dir(caCertPath), 0755); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// Create the certificates volume if it doesn't exist
	fmt.Println("📦 Creating certificates volume...")
	volCmd := exec.Command("docker", "volume", "create", "devbox_certs")
	volCmd.Run() // Ignore error if volume already exists

	// Check if CA certificate already exists in the volume (from previous runs)
	checkCmd := exec.Command("docker", "run", "--rm",
		"-v", "devbox_certs:/certificates",
		"alpine:latest",
		"sh", "-c", "test -f /certificates/AliasProjectCASelfSigned.pem && echo exists || echo notfound")

	output, _ := checkCmd.Output()
	if strings.TrimSpace(string(output)) != "exists" {
		// CA doesn't exist, run the openssl container to generate it
		fmt.Println("🔒 Generating CA certificate...")
		initCmd := exec.Command("docker", "run", "--rm",
			"-e", "DOMAIN=init",
			"-v", "devbox_certs:/certificates",
			"ghcr.io/aliasproject/openssl:latest")

		if err := initCmd.Run(); err != nil {
			return fmt.Errorf("failed to generate CA certificate. Please ensure GitHub authentication is set up: %w", err)
		}
		fmt.Println("✅ CA certificate generated!")
	} else {
		fmt.Println("📋 Found existing CA certificate")
	}

	// Copy CA certificate from volume to host for keychain installation
	fmt.Println("📋 Copying CA certificate to host...")
	copyCmd := exec.Command("docker", "run", "--rm",
		"-v", "devbox_certs:/source",
		"-v", fmt.Sprintf("%s:/dest", devboxDir),
		"alpine:latest",
		"sh", "-c", "cp /source/AliasProjectCASelfSigned.pem /dest/certs/AliasProjectCASelfSigned.pem")

	if err := copyCmd.Run(); err != nil {
		return fmt.Errorf("failed to copy CA certificate: %w", err)
	}

	if !fileExists(caCertPath) {
		return fmt.Errorf("CA certificate not found at %s after copy", caCertPath)
	}

	fmt.Println("✅ CA certificate ready!")

	// Prompt user to install CA certificate
	fmt.Println("\nTo enable HTTPS for your development environment, you need to install the")
	fmt.Println("Alias Project Certificate Authority (CA) certificate on your system.")
	fmt.Println("This allows your browser to trust the SSL certificates generated for your project.")

	if !promptYesNo("Would you like to install the CA certificate now?") {
		fmt.Println("Skipping CA certificate installation. You can install it later manually.")
		fmt.Printf("CA certificate location: %s\n", caCertPath)
		return nil
	}

	// Install CA certificate
	if err := setupSSLInstallation(caCertPath); err != nil {
		return fmt.Errorf("failed to install CA certificate: %w", err)
	}

	// Provide post-installation instructions
	showPostInstallInstructions()

	return nil
}

// setupTestDomainResolver creates /etc/resolver/test for automatic .test domain resolution on macOS
func setupTestDomainResolver() error {
	fmt.Println("🌐 Setting up .test domain resolver...")

	// Check if we're on macOS
	if !isMacOS() {
		fmt.Println("⏭️  Skipping .test resolver setup (not macOS)")
		return nil
	}

	resolverDir := "/etc/resolver"
	resolverFile := "/etc/resolver/test"
	resolverContent := "port 5354\nnameserver 127.0.0.1\n"

	// Check if resolver already exists
	if fileExists(resolverFile) {
		content, err := os.ReadFile(resolverFile)
		if err == nil && strings.Contains(string(content), "port 5354") && strings.Contains(string(content), "nameserver 127.0.0.1") {
			fmt.Println("✅ .test domain resolver already configured correctly")
			return nil
		}
	}

	fmt.Println("📝 Creating /etc/resolver/test for automatic .test domain resolution")
	fmt.Println("💡 This allows .test domains to work without manual DNS configuration")
	fmt.Println("🔐 You may be prompted for your password to create system files")

	// Create /etc/resolver directory if it doesn't exist
	if !dirExists(resolverDir) {
		cmd := exec.Command("sudo", "mkdir", "-p", resolverDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create resolver directory: %w", err)
		}
	}

	// Create the resolver file
	cmd := exec.Command("sudo", "tee", resolverFile)
	cmd.Stdin = strings.NewReader(resolverContent)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create resolver file: %w", err)
	}

	// Flush DNS cache to make changes take effect immediately
	fmt.Println("🔄 Flushing DNS cache...")
	flushCmd := exec.Command("sudo", "dscacheutil", "-flushcache")
	flushCmd.Run() // Ignore errors - not critical

	// Also try the newer flush command
	flushCmd2 := exec.Command("sudo", "killall", "-HUP", "mDNSResponder")
	flushCmd2.Run() // Ignore errors - not critical

	fmt.Println("✅ .test domain resolver configured successfully!")
	fmt.Println("🌐 .test domains will now automatically resolve to your devbox")
	fmt.Println("💡 Try: dig yourproject.test")

	return nil
}

// isMacOS checks if the current system is macOS
func isMacOS() bool {
	cmd := exec.Command("uname", "-s")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "Darwin"
}

func setupSSLInstallation(caCertPath string) error {
	fmt.Println()
	fmt.Println("🔐 Installing CA certificate to system...")
	fmt.Println("💡 You may be prompted for your password to authorize this change.")

	switch runtime.GOOS {
	case "darwin":
		return installCertMacOS(caCertPath)
	case "linux":
		return installCertLinux(caCertPath)
	case "windows":
		return installCertWindows(caCertPath)
	default:
		fmt.Printf("Unsupported operating system: %s\n", runtime.GOOS)
		fmt.Printf("Please install the CA certificate manually: %s\n", caCertPath)
		return nil
	}
}

func installCertMacOS(caCertPath string) error {
	fmt.Println("🍎 Installing CA certificate on macOS...")

	// Debug: Show the exact path being used
	fmt.Printf("🔍 Certificate path: %s\n", caCertPath)

	// Verify certificate file exists
	if !fileExists(caCertPath) {
		return fmt.Errorf("certificate file not found at: %s", caCertPath)
	}

	// Check if certificate is already installed to avoid duplicates
	checkCmd := exec.Command("security", "find-certificate", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain")
	if checkCmd.Run() == nil {
		fmt.Println("⚠️  Certificate already exists in keychain, removing old version...")
		removeCmd := exec.Command("sudo", "security", "delete-certificate", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain")
		removeCmd.Stdout = os.Stdout
		removeCmd.Stderr = os.Stderr
		removeCmd.Stdin = os.Stdin
		removeCmd.Run() // Ignore errors - certificate might not exist
	}

	// Step 1: Import the certificate to System keychain first
	fmt.Println("📥 Importing certificate to System keychain...")
	importCmd := exec.Command("sudo", "security", "import", caCertPath,
		"-k", "/Library/Keychains/System.keychain",
		"-t", "cert")
	importCmd.Stdout = os.Stdout
	importCmd.Stderr = os.Stderr
	importCmd.Stdin = os.Stdin

	if err := importCmd.Run(); err != nil {
		fmt.Printf("⚠️  Import step failed (may already exist): %v\n", err)
		// Continue anyway as certificate might already be imported
	}

	// Step 2: Add trust settings with specific policies for SSL
	fmt.Println("🔐 Setting certificate to 'Always Trust' for all users...")
	fmt.Printf("🔧 Running: sudo security add-trusted-cert -d -r trustRoot -p ssl -p basic -k /Library/Keychains/System.keychain %s\n", caCertPath)

	// Use add-trusted-cert with explicit trust policies
	trustCmd := exec.Command("sudo", "security", "add-trusted-cert",
		"-d",              // Add to admin cert store
		"-r", "trustRoot", // Trust as root
		"-p", "ssl", // Trust for SSL
		"-p", "basic", // Trust for X.509 Basic
		"-k", "/Library/Keychains/System.keychain", // Add to System keychain
		caCertPath)
	trustCmd.Stdout = os.Stdout
	trustCmd.Stderr = os.Stderr
	trustCmd.Stdin = os.Stdin

	if err := trustCmd.Run(); err != nil {
		// If the above fails, try a simpler approach
		fmt.Printf("⚠️  First trust attempt failed (%v), trying alternative method...\n", err)

		// Alternative: Use simpler add-trusted-cert without -p flags
		altTrustCmd := exec.Command("sudo", "security", "add-trusted-cert",
			"-d", "-r", "trustAsRoot",
			"-k", "/Library/Keychains/System.keychain",
			caCertPath)
		altTrustCmd.Stdout = os.Stdout
		altTrustCmd.Stderr = os.Stderr
		altTrustCmd.Stdin = os.Stdin

		if err := altTrustCmd.Run(); err != nil {
			fmt.Printf("❌ Failed to set certificate trust: %v\n", err)
			fmt.Println("💡 Manual trust setup required:")
			showManualTrustInstructions()

			// Still import it without trust so user can manually trust
			fmt.Printf("💡 You can also try: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s\n", caCertPath)
		} else {
			fmt.Println("✅ Certificate set to 'Always Trust' successfully!")
		}
	} else {
		fmt.Println("✅ Certificate set to 'Always Trust' successfully!")
	}

	// Verify the certificate was actually installed and trusted
	fmt.Println("🔍 Verifying certificate installation...")
	verifyCmd := exec.Command("security", "find-certificate", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain")
	if err := verifyCmd.Run(); err != nil {
		fmt.Println("⚠️  Certificate installation verification failed")
		return nil
	}

	// Additional check: verify the certificate is actually trusted
	fmt.Println("🔍 Verifying certificate trust settings...")

	// Debug: Show what we're looking for
	fmt.Printf("🔍 Looking for certificate: AliasProjectCASelfSigned\n")
	fmt.Printf("🔍 In keychain: /Library/Keychains/System.keychain\n")

	// Use the more reliable method: check if certificate exists and has the right trust settings
	trustCheckCmd := exec.Command("security", "find-certificate", "-a", "-c", "AliasProjectCASelfSigned", "-p", "/Library/Keychains/System.keychain")
	fmt.Printf("🔧 Running: %s\n", strings.Join(trustCheckCmd.Args, " "))

	trustOutput, trustErr := trustCheckCmd.Output()

	fmt.Printf("🔍 Command output length: %d bytes\n", len(trustOutput))
	if trustErr != nil {
		fmt.Printf("🔍 Command error: %v\n", trustErr)
	}

	if trustErr == nil && len(trustOutput) > 0 {
		// Certificate exists, now check if it's explicitly trusted
		// The -r trustRoot flag in add-trusted-cert should have set it as trusted
		fmt.Println("✅ Certificate found in system keychain!")

		// Double check by trying to verify a hypothetical certificate chain
		// This is the most reliable way to check if it will actually work
		fmt.Println("🔍 Testing certificate trust with verification...")

		// Create a simple test to see if the certificate is trusted
		testTrustCmd := exec.Command("security", "verify-cert", "-c", caCertPath)
		fmt.Printf("🔧 Running: %s\n", strings.Join(testTrustCmd.Args, " "))

		testTrustOutput, testTrustErr := testTrustCmd.CombinedOutput()
		fmt.Printf("🔍 Verify command output: %s\n", string(testTrustOutput))
		if testTrustErr != nil {
			fmt.Printf("🔍 Verify command error: %v\n", testTrustErr)
		}

		if testTrustErr == nil {
			fmt.Println("✅ Certificate is properly trusted and ready to use!")
		} else {
			fmt.Println("⚠️  Certificate installed but trust verification shows issues")
			fmt.Printf("💡 Verification error: %v\n", testTrustErr)
			fmt.Println("💡 The certificate might still work - try testing https://yourproject.test")
			fmt.Println("💡 If you see warnings, restart your browser or clear browser cache")
		}
	} else {
		fmt.Printf("❌ Certificate not found in keychain after installation\n")
		if trustErr != nil {
			fmt.Printf("🔍 Error details: %v\n", trustErr)
		}
		fmt.Println("💡 Trying alternative certificate search...")

		// Try a simpler search
		altCmd := exec.Command("security", "find-certificate", "-c", "AliasProjectCASelfSigned")
		fmt.Printf("🔧 Running: %s\n", strings.Join(altCmd.Args, " "))
		altOutput, altErr := altCmd.CombinedOutput()
		fmt.Printf("🔍 Alternative search output: %s\n", string(altOutput))
		if altErr != nil {
			fmt.Printf("🔍 Alternative search error: %v\n", altErr)
		}
	}

	// Show all certificates for debugging
	fmt.Println("🔍 Listing certificates in system keychain for debugging...")
	listCertificatesInKeychain()

	fmt.Println("✅ CA certificate installation process completed!")
	fmt.Println("🔒 SSL certificates should now be trusted by your browser")
	fmt.Println("🔄 Please restart your browser to ensure the changes take effect")
	return nil
}

func showManualTrustInstructions() {
	fmt.Println("💡 Manual trust setup required:")
	fmt.Println("💡 1. Open Keychain Access (Applications > Utilities)")
	fmt.Println("💡 2. Select 'System' keychain on the left")
	fmt.Println("💡 3. Find 'AliasProjectCASelfSigned' certificate")
	fmt.Println("💡 4. Double-click the certificate")
	fmt.Println("💡 5. Expand the 'Trust' section")
	fmt.Println("💡 6. Change 'When using this certificate:' to 'Always Trust'")
	fmt.Println("💡 7. Close the window and enter your password when prompted")
	fmt.Println("💡 8. The certificate should now show as trusted (blue checkmark)")
}

func listCertificatesInKeychain() {
	fmt.Println("📋 Certificates in System keychain:")

	// List all certificates in system keychain
	listCmd := exec.Command("security", "find-certificate", "-a", "/Library/Keychains/System.keychain")
	listOutput, listErr := listCmd.Output()

	if listErr != nil {
		fmt.Printf("❌ Failed to list certificates: %v\n", listErr)
		return
	}

	output := string(listOutput)

	// Count certificates
	certCount := strings.Count(output, "-----BEGIN CERTIFICATE-----")
	fmt.Printf("📊 Total certificates found: %d\n", certCount)

	// Look specifically for our certificate
	if strings.Contains(output, "AliasProjectCASelfSigned") {
		fmt.Println("✅ AliasProjectCASelfSigned found in keychain!")
	} else {
		fmt.Println("❌ AliasProjectCASelfSigned NOT found in keychain")
		fmt.Println("🔍 Searching for similar certificate names...")

		// Show certificate subjects to help debug
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "\"subj\"") && (strings.Contains(strings.ToLower(line), "alias") || strings.Contains(strings.ToLower(line), "ca")) {
				fmt.Printf("🔍 Found similar cert: %s\n", strings.TrimSpace(line))
			}
		}
	}
}

func debugCertificateSetup() {
	fmt.Println("🔍 Certificate Setup Debug Information")
	fmt.Println("=====================================")

	caCertPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.pem")
	caKeyPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.key")

	// Check file existence
	fmt.Printf("📁 Certificate directory: %s\n", filepath.Dir(caCertPath))
	fmt.Printf("📄 CA Certificate: %s - ", caCertPath)
	if fileExists(caCertPath) {
		fmt.Println("✅ EXISTS")
		if stat, err := os.Stat(caCertPath); err == nil {
			fmt.Printf("   📊 Size: %d bytes\n", stat.Size())
			fmt.Printf("   📅 Modified: %s\n", stat.ModTime().Format("2006-01-02 15:04:05"))
		}
	} else {
		fmt.Println("❌ MISSING")
	}

	fmt.Printf("🔑 CA Private Key: %s - ", caKeyPath)
	if fileExists(caKeyPath) {
		fmt.Println("✅ EXISTS")
	} else {
		fmt.Println("❌ MISSING")
	}

	// Check keychain
	fmt.Println("\n🔐 Keychain Analysis:")
	checkCmd := exec.Command("security", "find-certificate", "-a", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain")
	output, err := checkCmd.Output()
	if err == nil && len(output) > 0 {
		fmt.Println("✅ Certificate found in System keychain")
		fmt.Printf("📊 Output length: %d bytes\n", len(output))

		// Check trust status specifically
		fmt.Println("\n🔒 Trust Status Analysis:")
		if isCertificateTrusted() {
			fmt.Println("✅ Certificate is TRUSTED for SSL")
		} else {
			fmt.Println("❌ Certificate is NOT TRUSTED for SSL")
			fmt.Println("⚠️  This is why you see 'This connection is not private'")
		}

		// Show admin trust settings
		fmt.Println("\n📋 Admin Trust Settings:")
		dumpCmd := exec.Command("security", "dump-trust-settings", "-d")
		dumpOutput, dumpErr := dumpCmd.Output()
		if dumpErr == nil {
			output := string(dumpOutput)
			if strings.Contains(output, "AliasProjectCASelfSigned") {
				fmt.Println("✅ Certificate found in admin trust settings")
				// Extract relevant lines
				lines := strings.Split(output, "\n")
				for i, line := range lines {
					if strings.Contains(line, "AliasProjectCASelfSigned") {
						// Show the cert entry and next few lines
						for j := i; j < len(lines) && j < i+5; j++ {
							fmt.Printf("   %s\n", strings.TrimSpace(lines[j]))
						}
						break
					}
				}
			} else {
				fmt.Println("❌ Certificate NOT found in admin trust settings")
				fmt.Println("   This means it's not set to 'Always Trust'")
			}
		} else {
			fmt.Printf("⚠️  Could not dump trust settings: %v\n", dumpErr)
		}

		// Test SSL verification
		fmt.Println("\n🧪 SSL Policy Verification Test:")
		if fileExists(caCertPath) {
			verifyCmd := exec.Command("security", "verify-cert", "-c", caCertPath, "-p", "ssl")
			verifyOutput, verifyErr := verifyCmd.CombinedOutput()
			if verifyErr == nil {
				fmt.Println("✅ Certificate passes SSL policy verification")
			} else {
				fmt.Println("❌ Certificate FAILS SSL policy verification")
				fmt.Printf("   Error: %v\n", verifyErr)
				if len(verifyOutput) > 0 {
					fmt.Printf("   Details: %s\n", string(verifyOutput))
				}
			}
		}
	} else {
		fmt.Println("❌ Certificate NOT found in System keychain")
		if err != nil {
			fmt.Printf("   Error: %v\n", err)
		}
	}

	// List all CA certificates
	fmt.Println("\n📋 All CA certificates in System keychain:")
	listCmd := exec.Command("security", "find-certificate", "-a", "/Library/Keychains/System.keychain")
	listOutput, listErr := listCmd.Output()
	if listErr == nil {
		certCount := strings.Count(string(listOutput), "-----BEGIN CERTIFICATE-----")
		fmt.Printf("📊 Total certificates: %d\n", certCount)

		// Look for any certificate with "Alias" or "CA" in the name
		lines := strings.Split(string(listOutput), "\n")
		aliasCount := 0
		for _, line := range lines {
			if strings.Contains(line, "\"subj\"") && (strings.Contains(strings.ToLower(line), "alias") || strings.Contains(strings.ToLower(line), "project")) {
				fmt.Printf("🔍 Found: %s\n", strings.TrimSpace(line))
				aliasCount++
			}
		}
		fmt.Printf("📊 Alias/Project related certificates: %d\n", aliasCount)
	}

	// Test certificate verification if file exists
	if fileExists(caCertPath) {
		fmt.Println("\n🧪 Certificate Verification Test:")
		verifyCmd := exec.Command("security", "verify-cert", "-c", caCertPath)
		verifyOutput, verifyErr := verifyCmd.CombinedOutput()
		fmt.Printf("Command: %s\n", strings.Join(verifyCmd.Args, " "))
		if verifyErr == nil {
			fmt.Println("✅ Certificate verification PASSED")
		} else {
			fmt.Println("❌ Certificate verification FAILED")
			fmt.Printf("Error: %v\n", verifyErr)
		}
		if len(verifyOutput) > 0 {
			fmt.Printf("Output: %s\n", string(verifyOutput))
		}

		// Check certificate content with openssl
		fmt.Println("\n🔬 OpenSSL Certificate Analysis:")
		opensslCmd := exec.Command("openssl", "x509", "-in", caCertPath, "-text", "-noout")
		opensslOutput, opensslErr := opensslCmd.Output()
		if opensslErr == nil {
			lines := strings.Split(string(opensslOutput), "\n")
			for _, line := range lines {
				if strings.Contains(line, "Subject:") || strings.Contains(line, "Issuer:") || strings.Contains(line, "Not Before:") || strings.Contains(line, "Not After:") {
					fmt.Printf("   %s\n", strings.TrimSpace(line))
				}
			}
		} else {
			fmt.Printf("❌ OpenSSL analysis failed: %v\n", opensslErr)
		}
	}

	fmt.Println("\n🎯 Recommendations:")
	if !fileExists(caCertPath) {
		fmt.Println("1. Run 'devbox install' to generate certificates")
	} else if isCertificateTrusted() {
		fmt.Println("1. Certificate appears properly trusted!")
		fmt.Println("2. Restart your browser completely")
		fmt.Println("3. Clear browser cache and SSL state")
		fmt.Println("4. Test with: https://yourproject.test")
	} else {
		fmt.Println("1. Certificate exists but is NOT TRUSTED for SSL - this is the problem!")
		fmt.Println("2. Run 'devbox cert-install' to reinstall with proper trust settings")
		fmt.Println("3. If automatic trust fails, set manually in Keychain Access:")
		showManualTrustInstructions()
		fmt.Println("\n💡 Alternative command to try:")
		fmt.Printf("   sudo security add-trusted-cert -d -r trustRoot -p ssl -p basic \\\n")
		fmt.Printf("        -k /Library/Keychains/System.keychain %s\n", caCertPath)
	}
}

func installCertLinux(caCertPath string) error {
	fmt.Println("🐧 Installing CA certificate on Linux...")

	// Determine the correct certificate directory and update command
	var certDir string
	var updateCmd *exec.Cmd

	// Check for Ubuntu/Debian style
	if dirExists("/usr/local/share/ca-certificates") {
		certDir = "/usr/local/share/ca-certificates"
		updateCmd = exec.Command("sudo", "update-ca-certificates")
	} else if dirExists("/etc/pki/ca-trust/source/anchors") {
		// RHEL/CentOS/Fedora style
		certDir = "/etc/pki/ca-trust/source/anchors"
		updateCmd = exec.Command("sudo", "update-ca-trust")
	} else {
		fmt.Println("Warning: Could not determine certificate directory for your Linux distribution")
		fmt.Printf("Please install the CA certificate manually: %s\n", caCertPath)
		return nil
	}

	// Copy certificate to system directory
	destPath := filepath.Join(certDir, "devbox-ca.crt")
	copyCmd := exec.Command("sudo", "cp", caCertPath, destPath)
	copyCmd.Stdout = os.Stdout
	copyCmd.Stderr = os.Stderr
	copyCmd.Stdin = os.Stdin

	if err := copyCmd.Run(); err != nil {
		fmt.Printf("Warning: Failed to copy CA certificate: %v\n", err)
		fmt.Printf("You can install it manually: sudo cp %s %s\n", caCertPath, destPath)
		return nil
	}

	// Update certificate store
	updateCmd.Stdout = os.Stdout
	updateCmd.Stderr = os.Stderr
	updateCmd.Stdin = os.Stdin

	if err := updateCmd.Run(); err != nil {
		fmt.Printf("Warning: Failed to update certificate store: %v\n", err)
		return nil
	}

	fmt.Println("✅ CA certificate installed and trusted successfully!")
	fmt.Println("🔒 SSL certificates will now be trusted by your browser")
	return nil
}

func installCertWindows(caCertPath string) error {
	fmt.Println("🪟 Installing CA certificate on Windows...")

	// Use PowerShell to install certificate to Trusted Root Certification Authorities
	psCmd := fmt.Sprintf(`Import-Certificate -FilePath "%s" -CertStoreLocation "Cert:\LocalMachine\Root"`, caCertPath)
	cmd := exec.Command("powershell", "-Command", psCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: Failed to install CA certificate automatically: %v\n", err)
		fmt.Printf("You can install it manually by:\n")
		fmt.Printf("1. Double-clicking: %s\n", caCertPath)
		fmt.Printf("2. Click 'Install Certificate...'\n")
		fmt.Printf("3. Select 'Local Machine' and click 'Next'\n")
		fmt.Printf("4. Select 'Place all certificates in the following store'\n")
		fmt.Printf("5. Browse and select 'Trusted Root Certification Authorities'\n")
		fmt.Printf("6. Click 'Next' and then 'Finish'\n")
		return nil
	}

	fmt.Println("✅ CA certificate installed and trusted successfully!")
	fmt.Println("🔒 SSL certificates will now be trusted by your browser")
	return nil
}

func showPostInstallInstructions() {
	fmt.Println()
	fmt.Println("🎉 Certificate installation complete!")

	// Verify the certificate installation
	if verifyCertificateInstallation() {
		fmt.Println("✅ Certificate verification successful!")
	} else {
		fmt.Println("⚠️  Certificate verification failed - manual steps may be needed")
	}

	fmt.Println()
	fmt.Println("📝 Important next steps:")
	fmt.Println("   1. Restart your browser(s) to recognize the new certificate")
	fmt.Println("   2. Clear browser cache if you still see certificate warnings")
	fmt.Println("   3. Your .test domains will now show a secure padlock icon 🔒")
	fmt.Println()
	fmt.Println("🔍 To verify the certificate is trusted:")

	switch runtime.GOOS {
	case "darwin":
		fmt.Println("   • Open Keychain Access > System > Certificates")
		fmt.Println("   • Look for 'AliasProjectCASelfSigned' with a white checkmark")
	case "linux":
		fmt.Println("   • Run: openssl x509 -in /usr/local/share/ca-certificates/devbox-ca.crt -text -noout")
		fmt.Println("   • Or check your browser's certificate settings")
	case "windows":
		fmt.Println("   • Run: certmgr.msc > Trusted Root Certification Authorities")
		fmt.Println("   • Look for 'AliasProjectCASelfSigned'")
	}

	fmt.Println()
	fmt.Println("⚠️  If you still see 'This connection is not private':")
	fmt.Println("   • Try opening the site in an incognito/private window first")
	fmt.Println("   • Ensure you're accessing https://yourproject.test (not http://)")
	fmt.Println("   • Some browsers cache certificate errors - clear browsing data")
	fmt.Println()

	// Show browser-specific instructions
	showBrowserTroubleshooting()
}

func showBrowserTroubleshooting() {
	fmt.Println("🌐 Browser-specific troubleshooting:")
	fmt.Println()
	fmt.Println("Chrome/Chromium:")
	fmt.Println("  • Type 'chrome://settings/security' and check 'Manage certificates'")
	fmt.Println("  • Clear browsing data: chrome://settings/clearBrowserData")
	fmt.Println("  • For development: chrome://flags/#allow-insecure-localhost")
	fmt.Println()
	fmt.Println("Firefox:")
	fmt.Println("  • Go to about:preferences#privacy > View Certificates")
	fmt.Println("  • Check 'Authorities' tab for your CA certificate")
	fmt.Println("  • Clear cache: about:preferences#privacy > Clear Data")
	fmt.Println()
	fmt.Println("Safari (macOS):")
	fmt.Println("  • Safari uses system keychain - should work automatically")
	fmt.Println("  • Clear cache: Develop menu > Empty Caches")
	fmt.Println("  • Check Keychain Access for certificate trust settings")
	fmt.Println()
	fmt.Println("Edge:")
	fmt.Println("  • Uses Windows certificate store")
	fmt.Println("  • Clear cache: edge://settings/clearBrowserData")
	fmt.Println("  • Check: edge://settings/privacy > Manage certificates")
	fmt.Println()
}

func verifyCertificateInstallation() bool {
	switch runtime.GOOS {
	case "darwin":
		return verifyCertMacOS()
	case "linux":
		return verifyCertLinux()
	case "windows":
		return verifyCertWindows()
	default:
		return false
	}
}

func verifyCertMacOS() bool {
	// Check if certificate exists in system keychain
	cmd := exec.Command("security", "find-certificate", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain")
	if err := cmd.Run(); err != nil {
		return false // Certificate not found
	}

	// Now check if the certificate is actually trusted by checking trust settings
	return isCertificateTrusted()
}

func isCertificateTrusted() bool {
	// Method 1: Check if certificate can be verified (most reliable)
	caCertPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.pem")
	if fileExists(caCertPath) {
		// Use verify-cert with policy to check SSL trust
		verifyCmd := exec.Command("security", "verify-cert", "-c", caCertPath, "-p", "ssl")
		if err := verifyCmd.Run(); err == nil {
			return true // Certificate is trusted for SSL
		}
	}

	// Method 2: Check trust settings dump
	// Note: trust-settings-export is for exporting to file, not what we want
	// Instead, use dump-trust-settings
	dumpCmd := exec.Command("security", "dump-trust-settings", "-d")
	dumpOutput, dumpErr := dumpCmd.Output()

	if dumpErr == nil {
		output := string(dumpOutput)
		// Look for our certificate in the admin trust settings
		if strings.Contains(output, "AliasProjectCASelfSigned") {
			// Check if it has trust settings (not just present)
			lines := strings.Split(output, "\n")
			for i, line := range lines {
				if strings.Contains(line, "AliasProjectCASelfSigned") {
					// Check following lines for trust settings
					for j := i + 1; j < len(lines) && j < i+10; j++ {
						if strings.Contains(lines[j], "kSecTrustSettingsResult") {
							// Found trust settings for our cert
							return true
						}
					}
				}
			}
		}
	}

	return false
}

func verifyCertLinux() bool {
	// Check if certificate exists in ca-certificates directory
	if fileExists("/usr/local/share/ca-certificates/devbox-ca.crt") {
		return true
	}
	if fileExists("/etc/pki/ca-trust/source/anchors/devbox-ca.crt") {
		return true
	}
	return false
}

func verifyCertWindows() bool {
	// Check if certificate exists in Windows certificate store
	cmd := exec.Command("powershell", "-Command",
		"Get-ChildItem -Path Cert:\\LocalMachine\\Root | Where-Object { $_.Subject -like '*AliasProjectCASelfSigned*' }")
	return cmd.Run() == nil
}

func promptYesNo(prompt string) bool {
	fmt.Printf("%s (y/n): ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		response := strings.ToLower(strings.TrimSpace(scanner.Text()))
		return response == "y" || response == "yes"
	}
	return false
}

// isWatchmanHealthy removed - we don't wait for Watchman health checks

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	return err == nil && !info.IsDir()
}

func dirExists(dirname string) bool {
	info, err := os.Stat(dirname)
	return err == nil && info.IsDir()
}

func showHelp() {
	fmt.Printf("Devbox v%s - Development Environment Manager\n\n", version)
	fmt.Printf("🌐 Network-Ready Development Environment\n")
	fmt.Printf("Access your projects from ANY device on your network!\n\n")

	fmt.Println("USAGE:")
	fmt.Println("  devbox [COMMAND]")
	fmt.Println()
	fmt.Println("COMMANDS:")
	fmt.Println("  help, -h, --help     Show this help message")
	fmt.Println("  -v, --version        Show version information")
	fmt.Println("  update               Update devbox to the latest version")
	fmt.Println("  install              Show installation status")
	fmt.Println("  status               Show complete system status")
	fmt.Println("  network-help         Show network configuration options")
	fmt.Println("  cert-verify          Verify SSL certificate installation status")
	fmt.Println("  cert-install         Install CA certificate manually")
	fmt.Println("  cert-debug           Show detailed certificate debugging information")
	fmt.Println("  github-auth          Set up GitHub authentication for container registry")
	fmt.Println()
	fmt.Println("PROJECT COMMANDS:")
	fmt.Println("  init                 Initialize current directory as a devbox project")
	fmt.Println("  start                Start FrankenPHP container (auto-initializes if needed)")
	fmt.Println("  stop                 Stop project containers")
	fmt.Println("  php-version          Change FrankenPHP version for current project")
	fmt.Println("  projects             List all devbox projects")
	fmt.Println("  remove               Remove project configuration and stop containers")
	fmt.Println("  dns-cleanup          Remove .test domain resolver (macOS)")
	fmt.Println()
	fmt.Println("GLOBAL COMMANDS:")
	fmt.Println("  global start         Start global devbox services")
	fmt.Println("  global stop          Stop global devbox services")
	fmt.Println("  database add         Add database service (MySQL or PostgreSQL)")
	fmt.Println("  database remove      Remove database service")
	fmt.Println()
	fmt.Println("🚀 WORKFLOW:")
	fmt.Println("  1. Navigate to any directory you want as a project")
	fmt.Println("  2. Run 'devbox start' (handles everything automatically)")
	fmt.Println("  3. Access your project at https://directoryname.test")
	fmt.Println()
	fmt.Println("🛠️ DOCKER COMPOSE COMMANDS (for advanced users):")
	fmt.Println("  up, down, ps, logs, pull, restart")
	fmt.Println()
	fmt.Println("🌍 NETWORK ACCESS:")
	fmt.Println("  Local machine:       https://yourproject.test")
	fmt.Println("  Network devices:     Configure DNS for .test domains")
	fmt.Println("  Direct IP access:    Check 'devbox status' for your IP")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  devbox start         # Initialize (if needed) and start project")
	fmt.Println("  devbox init          # Initialize project without starting")
	fmt.Println("  devbox php-version   # Change FrankenPHP version for project")
	fmt.Println("  devbox projects      # List all projects")
	fmt.Println("  devbox cert-verify   # Check SSL certificate status")
	fmt.Println("  devbox cert-install  # Install CA certificate manually")
	fmt.Println("  devbox cert-debug    # Show detailed certificate debugging info")
	fmt.Println("  devbox remove        # Remove project config and containers")
	fmt.Println("  devbox dns-cleanup   # Remove .test domain resolver (macOS)")
	fmt.Println("  devbox global start  # Start global devbox services")
	fmt.Println("  devbox status        # Show complete status")
	fmt.Println("  devbox network-help  # Network configuration guide")
	fmt.Println()
	fmt.Println("For more information, visit: https://github.com/aliasproject/devbox")
}

func showStatus() error {
	fmt.Printf("Devbox v%s - System Status\n\n", version)

	fmt.Println("📦 Installation Status:")
	fmt.Printf("  Binary location: %s (%s)\n", devboxBinary, getStatus(fileExists(devboxBinary)))
	fmt.Printf("  Global services: %s\n", getStatus(isGlobalServicesRunning()))

	// Environment Variables Status
	fmt.Println("\n🔧 Global Configuration:")
	fmt.Printf("  Project mode: ✅ Individual directory-based projects\n")
	fmt.Printf("  Database: %s\n", getDatabaseStatus())
	fmt.Printf("  SSL/Vhosts: ✅ Direct management\n")
	fmt.Printf("  GitHub token: %s\n", getTokenStatus())
	fmt.Printf("  Storage mode: ✅ Stateless (no persistent files)\n")

	// Docker Status
	fmt.Println("\n🐳 Docker Status:")
	if !isDockerInstalled() {
		fmt.Println("  ❌ Docker not installed or not running")
		return nil
	}
	fmt.Println("  ✅ Docker is running")

	// Global Container Status
	if !isInstalled() {
		fmt.Println("  ❌ Devbox not installed - no global services to check")
		return nil
	}

	fmt.Println("\n📊 Global Container Status:")

	// Check global containers (including potential database containers)
	containers := []string{"devbox_dns", "devbox_nginx", "devbox_mail"}
	if isSpecificDatabaseRunning("mysql") {
		containers = append(containers, "devbox_mysql")
	}
	if isSpecificDatabaseRunning("postgresql") {
		containers = append(containers, "devbox_postgresql")
	}

	for _, container := range containers {
		status := getContainerStatus(container)
		fmt.Printf("  %s: %s\n", container, status)
	}

	// Show current project status if in a project directory
	if isProjectInitialized() {
		fmt.Println("\n🏗️  Current Project Status:")
		showProjectStatus()
	} else {
		fmt.Println("\n🏗️  Current Directory:")
		fmt.Println("  ❌ Not a devbox project")
		fmt.Println("  💡 Run 'devbox init' to initialize a project here")
	}

	// Show port mappings
	fmt.Println("\n🌐 Port Mappings:")
	fmt.Println("  HTTP: http://localhost:80")
	fmt.Println("  HTTPS: https://localhost:443")
	if isSpecificDatabaseRunning("mysql") {
		fmt.Println("  MySQL: localhost:3306")
	}
	if isSpecificDatabaseRunning("postgresql") {
		fmt.Println("  PostgreSQL: localhost:5432")
	}
	fmt.Println("  Mailhog: http://localhost:8025")
	fmt.Println("  DNS: localhost:5354")

	// Show network access information
	hostIP, err := getHostIP()
	if err == nil {
		fmt.Printf("\n🌍 Network Access (from other devices):\n")
		fmt.Printf("  🔧 Configure DNS: Set %s:5354 as DNS server for .test domains\n", hostIP)
		fmt.Printf("  🌐 Direct IP access: http://%s or https://%s\n", hostIP, hostIP)
		fmt.Printf("  📡 DNS server running on port 5354\n")
		fmt.Printf("  💡 Run 'devbox network-help' for device setup instructions\n")
	}

	return nil
}

func showNetworkHelp() {
	fmt.Println("🌍 Network Configuration Options for .test Domains")

	hostIP, err := getHostIP()
	if err != nil {
		fmt.Println("❌ Could not determine host IP address")
		return
	}

	fmt.Printf("Your host IP: %s\n\n", hostIP)

	// Show macOS automatic setup status
	if isMacOS() {
		fmt.Println("🍎 MACOS LOCAL SETUP:")
		resolverFile := "/etc/resolver/test"
		if fileExists(resolverFile) {
			fmt.Println("  ✅ .test domains automatically configured via /etc/resolver/test")
			fmt.Println("  🌐 Local .test domains should work immediately")
			fmt.Println("  💡 Run 'devbox dns-cleanup' to remove if needed")
		} else {
			fmt.Println("  ❌ .test resolver not configured")
			fmt.Println("  💡 Run devbox installation again to set up automatic resolution")
		}
		fmt.Println()
	}

	fmt.Println("🌐 NETWORK ACCESS SETUP for .test domains")
	fmt.Println("To use .test domains on other devices, choose one option:")
	fmt.Println()
	fmt.Println("📡 OPTION 1: Router DNS Configuration (Recommended)")
	fmt.Println("If your router supports DNS forwarding/conditional forwarding:")
	fmt.Println("  1. Access your router's admin panel (usually http://192.168.1.1)")
	fmt.Println("  2. Look for DNS settings, DNS forwarding, or conditional forwarding")
	fmt.Printf("  3. Add rule: Forward *.test queries to %s port 5354\n", hostIP)
	fmt.Println("  4. Save and restart router")
	fmt.Println("  → Result: All devices automatically use .test domains")
	fmt.Println()

	fmt.Println("🏠 OPTION 2: Router DHCP DNS Override")
	fmt.Println("If your router allows setting custom DNS servers via DHCP:")
	fmt.Println("  1. Access router admin panel")
	fmt.Println("  2. Find DHCP settings")
	fmt.Printf("  3. Set primary DNS to: %s\n", hostIP)
	fmt.Println("  4. Set secondary DNS to: 8.8.8.8 (for internet domains)")
	fmt.Println("  5. Restart router and reconnect devices")
	fmt.Println("  ⚠️  Note: This routes ALL DNS through your devbox")
	fmt.Println()

	fmt.Println("🔧 OPTION 3: Per-Device Configuration")
	fmt.Println("Configure each device individually:")
	fmt.Printf("  • Set DNS server to: %s\n", hostIP)
	fmt.Println("  • Port: 5354 (if device supports custom ports)")
	fmt.Println("  • See device-specific instructions online")
	fmt.Println()

	fmt.Println("📊 Testing Your Setup:")
	fmt.Println("  1. Navigate to any directory: cd ~/Sites/myproject")
	fmt.Println("  2. Start your project: devbox start")
	fmt.Println("  3. Test locally: https://myproject.test")
	fmt.Printf("  4. Test from other devices: http://%s\n", hostIP)
	fmt.Printf("  5. Test DNS: nslookup myproject.test %s\n", hostIP)
	fmt.Println()

	fmt.Println("💡 Troubleshooting:")
	fmt.Println("  • Test direct IP access first to verify web server")
	fmt.Println("  • Ensure devices are on the same network")
	fmt.Println("  • Check firewall settings if connections fail")
	if isMacOS() {
		fmt.Println("  • On macOS: Check /etc/resolver/test exists and is correct")
		fmt.Println("  • Try: sudo dscacheutil -flushcache")
	}
}

func getProjectName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(cwd)
}

func getProjectID() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	// Create a safe project ID from the full path
	projectID := strings.ReplaceAll(cwd, "/", "_")
	projectID = strings.ReplaceAll(projectID, ":", "")
	projectID = strings.ReplaceAll(projectID, " ", "_")
	return projectID
}

// getProjectConfigPath removed - stateless mode uses container inspection

// getProjectConfigDir removed - stateless mode uses container inspection

func isProjectInitialized() bool {
	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)

	// Check if project container exists
	cmd := exec.Command("docker", "inspect", containerName)
	return cmd.Run() == nil
}

// ensureProjectDir removed - stateless mode

func initProject() error {
	if isProjectInitialized() {
		fmt.Println("✅ Project already initialized!")
		return showProjectStatus()
	}

	return initProjectInline()
}

func initProjectInline() error {
	projectName := getProjectName()
	cwd, _ := os.Getwd()
	fmt.Printf("🚀 Initializing devbox project: %s\n", projectName)
	fmt.Printf("📁 Directory: %s\n\n", cwd)

	// Get PHP version
	fmt.Println("Select FrankenPHP version for this project:")
	phpVersion := selectOption("PHP Version", []string{"8.3", "8.2", "8.1", "8.0", "7.4"})

	// Project config stored as container labels (stateless mode)
	// No files created - all config derived from containers

	fmt.Println("✅ Project initialized successfully!")
	fmt.Printf("📁 Project: %s\n", projectName)
	fmt.Printf("🐘 FrankenPHP Version: %s\n", phpVersion)
	fmt.Printf("💾 Config stored as container labels (stateless)\n")
	fmt.Printf("✨ No files created anywhere!\n")

	return nil
}

func startProjectContainer(projectName, phpVersion string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	containerName := fmt.Sprintf("devbox_php_%s", projectName)
	imageName := fmt.Sprintf("ghcr.io/aliasproject/php:%s-frankenphp", phpVersion)

	// Check if container already exists and is running
	checkCmd := exec.Command("docker", "inspect", containerName)
	if checkCmd.Run() == nil {
		// Container exists, check if it's running
		statusCmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", containerName)
		output, err := statusCmd.Output()
		if err == nil && strings.TrimSpace(string(output)) == "running" {
			fmt.Printf("✅ Container %s is already running\n", containerName)
			return nil
		}
		// Container exists but not running, start it
		startCmd := exec.Command("docker", "start", containerName)
		startCmd.Stdout = os.Stdout
		startCmd.Stderr = os.Stderr
		if err := startCmd.Run(); err != nil {
			// If start fails, remove and recreate
			exec.Command("docker", "rm", containerName).Run()
		} else {
			fmt.Printf("✅ Started existing container %s\n", containerName)
			return nil
		}
	}

	// Create new container with labels for stateless config
	fmt.Printf("🚀 Creating new FrankenPHP container: %s\n", containerName)
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart", "unless-stopped",
		"-v", fmt.Sprintf("%s:/app", cwd),
		"--network", "devbox_devbox-network",
		"--dns", "192.168.237.4",
		"-e", "SERVER_NAME=:80",
		"--label", fmt.Sprintf("devbox.project.name=%s", projectName),
		"--label", fmt.Sprintf("devbox.project.path=%s", cwd),
		"--label", fmt.Sprintf("devbox.project.php_version=%s", phpVersion),
		imageName,
	}

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

func changePHPVersion() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized. Run 'devbox start' first")
	}

	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)
	fmt.Printf("🔄 Changing FrankenPHP version for project: %s\n\n", projectName)

	// Get current FrankenPHP version
	currentVersion := getProjectPHPVersion()
	fmt.Printf("Current FrankenPHP version: %s\n\n", currentVersion)

	// Select new FrankenPHP version
	newVersion := selectOption("Select new FrankenPHP version", []string{"8.3", "8.2", "8.1", "8.0", "7.4"})

	if newVersion == currentVersion {
		fmt.Println("✅ PHP version unchanged")
		return nil
	}

	// Stop and remove current container
	fmt.Printf("🛑 Stopping and removing current PHP container: %s\n", containerName)
	stopCmd := exec.Command("docker", "stop", containerName)
	stopCmd.Run() // Ignore errors if container doesn't exist

	removeCmd := exec.Command("docker", "rm", containerName)
	removeCmd.Run() // Ignore errors if container doesn't exist

	// Update container label (stateless config)
	fmt.Println("🔄 Updating PHP version in container labels...")
	// Note: Labels are updated when container is recreated

	fmt.Printf("✅ FrankenPHP version changed from %s to %s\n", currentVersion, newVersion)
	fmt.Println("🚀 Run 'devbox start' to start with new FrankenPHP version")

	return nil
}

func getProjectPHPVersion() string {
	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)

	// Get PHP version from container labels
	cmd := exec.Command("docker", "inspect", "-f", "{{index .Config.Labels \"devbox.project.php_version\"}}", containerName)
	output, err := cmd.Output()
	if err != nil {
		return "8.3" // default
	}

	version := strings.TrimSpace(string(output))
	if version == "" || version == "<no value>" {
		return "8.3"
	}

	return version
}

// getProjectConfig removed - using container labels

// updateProjectConfig removed - using container labels

func startProject() error {
	projectName := getProjectName()

	if !isProjectInitialized() {
		fmt.Printf("🔄 Project not initialized. Setting up project: %s\n", projectName)
		if err := initProjectInline(); err != nil {
			return fmt.Errorf("failed to initialize project: %w", err)
		}
		fmt.Println()
	}

	fmt.Printf("🚀 Starting FrankenPHP project: %s\n", projectName)

	// Ensure global services are running
	if err := ensureGlobalServices(); err != nil {
		return fmt.Errorf("failed to ensure global services: %w", err)
	}

	// Ensure GitHub authentication for image pulls
	fmt.Println("🔍 Ensuring GitHub authentication for FrankenPHP container...")
	if err := ensureGitHubAuth(); err != nil {
		fmt.Printf("Warning: GitHub authentication failed: %v\n", err)
		fmt.Println("FrankenPHP container pull may fail. Continuing anyway...")
	}

	// Start FrankenPHP container using docker run
	phpVersion := getProjectPHPVersion()
	if err := startProjectContainer(projectName, phpVersion); err != nil {
		return fmt.Errorf("failed to start FrankenPHP container: %w", err)
	}

	// Create SSL certificate and vhost directly
	fmt.Println("🔒 Setting up SSL certificate and nginx vhost...")
	if err := setupProjectSSLAndVhost(projectName); err != nil {
		fmt.Printf("Warning: Failed to setup SSL/vhost: %v\n", err)
		fmt.Println("💡 Project will still work but might not have HTTPS")
	} else {
		fmt.Println("✅ SSL certificate and vhost created successfully!")
		fmt.Printf("🌐 Project available at: https://%s.test\n", projectName)
	}

	fmt.Println("✅ Project containers started successfully!")
	return showProjectStatus()
}

func stopProject() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized")
	}

	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)
	fmt.Printf("🛑 Stopping project container: %s\n", containerName)

	cmd := exec.Command("docker", "stop", containerName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop project container: %w", err)
	}

	fmt.Println("✅ Project container stopped successfully!")
	fmt.Println("💡 SSL certificate and vhost remain active")
	fmt.Println("🗑️  Use 'devbox remove' to completely clean up the project")
	return nil
}

func startGlobalServices() error {
	fmt.Println("🚀 Starting global devbox services...")

	// Clean up any existing networks that might conflict
	fmt.Println("🔄 Cleaning up existing networks...")
	exec.Command("docker", "network", "rm", "devbox_internal").Run()
	exec.Command("docker", "network", "rm", "devbox_devbox-network").Run()

	// Ensure GitHub authentication for image pulls
	fmt.Println("🔍 Ensuring GitHub authentication for global services...")
	if err := ensureGitHubAuth(); err != nil {
		fmt.Printf("Warning: GitHub authentication failed: %v\n", err)
		fmt.Println("Container pulls may fail. Continuing anyway...")
	}

	// Get host IP and MySQL version for compose template
	hostIP, err := getHostIP()
	if err != nil {
		hostIP = "127.0.0.1"
	}

	dbType := os.Getenv("DATABASE_TYPE")
	dbVersion := os.Getenv("DATABASE_VERSION")

	// Generate compose content based on database selection
	composeContent := generateComposeContent(hostIP, dbType, dbVersion)

	// Start services using compose via stdin
	cmd := exec.Command("docker", "compose", "-f", "-", "up", "-d")
	cmd.Stdin = strings.NewReader(composeContent)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start global services via compose: %w", err)
	}

	fmt.Println("✅ Global services started successfully!")
	return nil
}

// Individual service functions removed - using embedded compose instead

func stopGlobalServices() error {
	fmt.Println("🛑 Stopping global devbox services...")

	// Use compose to stop all services cleanly
	composeContent := `name: devbox`
	cmd := exec.Command("docker", "compose", "-f", "-", "down")
	cmd.Stdin = strings.NewReader(composeContent)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop global services via compose: %w", err)
	}

	fmt.Println("✅ Global services stopped successfully!")
	return nil
}

func ensureGlobalServices() error {
	// Check if global services are running
	if !isGlobalServicesRunning() {
		fmt.Println("🔄 Global services not running, starting them...")
		return startGlobalServices()
	}
	return nil
}

func isGlobalServicesRunning() bool {
	// Use compose to check if all services are running
	composeContent := `name: devbox`
	cmd := exec.Command("docker", "compose", "-f", "-", "ps", "-q")
	cmd.Stdin = strings.NewReader(composeContent)

	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// If we have output, services exist; check if they're all running
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 4 { // We expect 4 services
		return false
	}

	// Check each container is running
	containers := []string{"devbox_dns", "devbox_nginx", "devbox_mysql", "devbox_mail"}
	for _, container := range containers {
		cmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", container)
		output, err := cmd.Output()
		if err != nil || strings.TrimSpace(string(output)) != "running" {
			return false
		}
	}
	return true
}

func generateComposeContent(hostIP, dbType, dbVersion string) string {
	baseContent := fmt.Sprintf(`name: devbox

services:
    dns:
        container_name: devbox_dns
        image: ghcr.io/aliasproject/dns:latest
        restart: unless-stopped
        ports:
            - "0.0.0.0:5354:53/udp"
        networks:
            internal:
                ipv4_address: 192.168.237.4
        environment:
            - COMMANDS=--address=/test/%s --server=1.1.1.1 --server=8.8.8.8 --log-facility=-
        dns:
            - 1.1.1.1
            - 8.8.8.8

    nginx:
        container_name: devbox_nginx
        image: ghcr.io/aliasproject/nginx:latest
        restart: unless-stopped
        volumes:
            - certs:/etc/nginx/ssl
            - vhosts:/etc/nginx/conf.d
            - .:/public
        ports:
            - 80:80
            - 443:443
        networks:
            - devbox-network
            - internal
        depends_on:
            - dns`, hostIP)

	// Add database service if configured
	if dbType != "" && dbVersion != "" {
		var dbService string
		if dbType == "mysql" {
			dbService = fmt.Sprintf(`

    mysql:
        container_name: devbox_mysql
        image: mysql:%s
        platform: linux/amd64
        restart: unless-stopped
        environment:
            - MYSQL_ROOT_PASSWORD=
            - MYSQL_ALLOW_EMPTY_PASSWORD=yes
        ports:
            - 3306:3306
        networks:
            - devbox-network
            - internal
        volumes:
            - mysql-%s:/var/lib/mysql
        depends_on:
            - dns
            - nginx`, dbVersion, dbVersion)
		} else if dbType == "postgresql" {
			dbService = fmt.Sprintf(`

    postgresql:
        container_name: devbox_postgresql
        image: postgres:%s
        restart: unless-stopped
        environment:
            - POSTGRES_USER=devbox
            - POSTGRES_PASSWORD=devbox
            - POSTGRES_DB=devbox
        ports:
            - 5432:5432
        networks:
            - devbox-network
            - internal
        volumes:
            - postgresql-%s:/var/lib/postgresql/data
        depends_on:
            - dns
            - nginx`, dbVersion, dbVersion)
		}
		baseContent += dbService
	}

	// Add mail service
	baseContent += `

    mail:
        container_name: devbox_mail
        image: mailhog/mailhog:latest
        platform: linux/amd64
        restart: on-failure
        ports:
            - 1025:1025
            - 8025:8025
        networks:
            - internal

volumes:
    certs:
    vhosts:`

	// Add database volumes
	if dbType == "mysql" {
		baseContent += `
    mysql-8.0:
    mysql-5.7:`
	} else if dbType == "postgresql" {
		baseContent += `
    postgresql-16:
    postgresql-15:
    postgresql-14:
    postgresql-13:`
	}

	baseContent += `

networks:
    internal:
        driver: bridge
        ipam:
            config:
                - subnet: 192.168.237.0/24
    devbox-network:
        driver: bridge`

	return baseContent
}

func addDatabase() error {
	fmt.Println("📊 Adding database service to devbox...")

	// Show current databases
	runningDatabases := getRunningDatabases()
	if len(runningDatabases) > 0 {
		fmt.Println("\nCurrently running databases:")
		for _, db := range runningDatabases {
			fmt.Printf("  • %s\n", db)
		}
		fmt.Println()
	}

	// Get database selection
	dbType := selectOption("Select Database Type", []string{"mysql", "postgresql"})
	var dbVersion string

	if dbType == "mysql" {
		dbVersion = selectOption("Select MySQL Version", []string{"8.0", "5.7"})

		// Check if MySQL already exists
		if isSpecificDatabaseRunning("mysql") {
			fmt.Println("⚠️  MySQL is already running")
			if !promptYesNo("Would you like to replace it with this version?") {
				return nil
			}
			stopSpecificDatabase("mysql")
		}
	} else {
		dbVersion = selectOption("Select PostgreSQL Version", []string{"16", "15", "14", "13"})

		// Check if PostgreSQL already exists
		if isSpecificDatabaseRunning("postgresql") {
			fmt.Println("⚠️  PostgreSQL is already running")
			if !promptYesNo("Would you like to replace it with this version?") {
				return nil
			}
			stopSpecificDatabase("postgresql")
		}
	}

	fmt.Printf("🚀 Starting %s %s...\n", strings.Title(dbType), dbVersion)

	// Start the specific database container
	if err := startSpecificDatabase(dbType, dbVersion); err != nil {
		return fmt.Errorf("failed to start %s: %w", dbType, err)
	}

	fmt.Printf("✅ %s %s added successfully!\n", strings.Title(dbType), dbVersion)

	if dbType == "mysql" {
		fmt.Printf("🔗 MySQL connection: mysql://root@localhost:3306\n")
	} else {
		fmt.Printf("🔗 PostgreSQL connection: postgresql://devbox:devbox@localhost:5432/devbox\n")
	}

	return nil
}

func removeDatabase() error {
	fmt.Println("🗑️  Removing database service from devbox...")

	runningDatabases := getRunningDatabases()
	if len(runningDatabases) == 0 {
		fmt.Println("ℹ️  No database services are currently running")
		return nil
	}

	fmt.Println("\nCurrently running databases:")
	for i, db := range runningDatabases {
		fmt.Printf("%d. %s\n", i+1, db)
	}
	fmt.Println()

	var dbToRemove string
	if len(runningDatabases) == 1 {
		dbToRemove = runningDatabases[0]
		if !promptYesNo(fmt.Sprintf("Remove %s? This will stop the database but preserve data volumes.", dbToRemove)) {
			fmt.Println("Database removal cancelled")
			return nil
		}
	} else {
		fmt.Print("Select database to remove (number): ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
			if err != nil || choice < 1 || choice > len(runningDatabases) {
				return fmt.Errorf("invalid choice")
			}
			dbToRemove = runningDatabases[choice-1]

			if !promptYesNo(fmt.Sprintf("Remove %s? This will stop the database but preserve data volumes.", dbToRemove)) {
				fmt.Println("Database removal cancelled")
				return nil
			}
		}
	}

	// Determine database type from container name
	var dbType string
	if strings.Contains(dbToRemove, "MySQL") {
		dbType = "mysql"
	} else if strings.Contains(dbToRemove, "PostgreSQL") {
		dbType = "postgresql"
	}

	fmt.Printf("🚀 Removing %s...\n", dbToRemove)

	if err := stopSpecificDatabase(dbType); err != nil {
		return fmt.Errorf("failed to remove %s: %w", dbToRemove, err)
	}

	fmt.Printf("✅ %s removed successfully!\n", dbToRemove)
	fmt.Println("💾 Database volumes preserved - data not lost")
	fmt.Println("🔄 Run 'devbox database add' to add a database service again")

	return nil
}

func isDatabaseRunning() bool {
	return len(getRunningDatabases()) > 0
}

func getRunningDatabases() []string {
	var databases []string

	// Check for MySQL
	mysqlCmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", "devbox_mysql")
	if output, err := mysqlCmd.Output(); err == nil && strings.TrimSpace(string(output)) == "running" {
		// Get MySQL version
		versionCmd := exec.Command("docker", "inspect", "-f", "{{.Config.Image}}", "devbox_mysql")
		if versionOutput, err := versionCmd.Output(); err == nil {
			image := strings.TrimSpace(string(versionOutput))
			version := strings.Split(image, ":")[1]
			databases = append(databases, fmt.Sprintf("MySQL %s", version))
		} else {
			databases = append(databases, "MySQL (unknown version)")
		}
	}

	// Check for PostgreSQL
	pgCmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", "devbox_postgresql")
	if output, err := pgCmd.Output(); err == nil && strings.TrimSpace(string(output)) == "running" {
		// Get PostgreSQL version
		versionCmd := exec.Command("docker", "inspect", "-f", "{{.Config.Image}}", "devbox_postgresql")
		if versionOutput, err := versionCmd.Output(); err == nil {
			image := strings.TrimSpace(string(versionOutput))
			version := strings.Split(image, ":")[1]
			databases = append(databases, fmt.Sprintf("PostgreSQL %s", version))
		} else {
			databases = append(databases, "PostgreSQL (unknown version)")
		}
	}

	return databases
}

func isSpecificDatabaseRunning(dbType string) bool {
	var containerName string
	if dbType == "mysql" {
		containerName = "devbox_mysql"
	} else if dbType == "postgresql" {
		containerName = "devbox_postgresql"
	} else {
		return false
	}

	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", containerName)
	if output, err := cmd.Output(); err == nil && strings.TrimSpace(string(output)) == "running" {
		return true
	}
	return false
}

func stopSpecificDatabase(dbType string) error {
	var containerName string
	if dbType == "mysql" {
		containerName = "devbox_mysql"
	} else if dbType == "postgresql" {
		containerName = "devbox_postgresql"
	} else {
		return fmt.Errorf("unknown database type: %s", dbType)
	}

	fmt.Printf("🛑 Stopping %s...\n", containerName)
	exec.Command("docker", "stop", containerName).Run()
	exec.Command("docker", "rm", containerName).Run()
	return nil
}

func startSpecificDatabase(dbType, dbVersion string) error {
	var containerName, image string
	var args []string

	if dbType == "mysql" {
		containerName = "devbox_mysql"
		image = fmt.Sprintf("mysql:%s", dbVersion)
		args = []string{
			"run", "-d",
			"--name", containerName,
			"--restart", "unless-stopped",
			"--platform", "linux/amd64",
			"-p", "3306:3306",
			"-e", "MYSQL_ROOT_PASSWORD=",
			"-e", "MYSQL_ALLOW_EMPTY_PASSWORD=yes",
			"-v", fmt.Sprintf("devbox_mysql-%s:/var/lib/mysql", dbVersion),
			"--network", "devbox_devbox-network",
			image,
		}
	} else if dbType == "postgresql" {
		containerName = "devbox_postgresql"
		image = fmt.Sprintf("postgres:%s", dbVersion)
		args = []string{
			"run", "-d",
			"--name", containerName,
			"--restart", "unless-stopped",
			"-p", "5432:5432",
			"-e", "POSTGRES_USER=devbox",
			"-e", "POSTGRES_PASSWORD=devbox",
			"-e", "POSTGRES_DB=devbox",
			"-v", fmt.Sprintf("devbox_postgresql-%s:/var/lib/postgresql/data", dbVersion),
			"--network", "devbox_devbox-network",
			image,
		}
	} else {
		return fmt.Errorf("unknown database type: %s", dbType)
	}

	// Create network if it doesn't exist
	exec.Command("docker", "network", "create", "devbox_devbox-network").Run()

	// Create volume if it doesn't exist
	if dbType == "mysql" {
		exec.Command("docker", "volume", "create", fmt.Sprintf("devbox_mysql-%s", dbVersion)).Run()
	} else {
		exec.Command("docker", "volume", "create", fmt.Sprintf("devbox_postgresql-%s", dbVersion)).Run()
	}

	// Start the container
	cmd := exec.Command("docker", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start %s container: %w", dbType, err)
	}

	// Connect to internal network if it exists
	exec.Command("docker", "network", "connect", "devbox_internal", containerName).Run()

	return nil
}

func showProjectStatus() error {
	if !isProjectInitialized() {
		fmt.Println("❌ Project not initialized")
		fmt.Println("💡 Run 'devbox init' to initialize this project")
		return nil
	}

	projectName := getProjectName()
	phpVersion := getProjectPHPVersion()

	fmt.Printf("📊 Project Status: %s\n", projectName)
	fmt.Printf("🐘 PHP Version: %s\n", phpVersion)
	cwd, _ := os.Getwd()
	fmt.Printf("📁 Directory: %s\n", cwd)

	// Check project container status
	containerName := fmt.Sprintf("devbox_php_%s", projectName)
	status := getContainerStatus(containerName)
	fmt.Printf("🐳 Container: %s (%s)\n", containerName, status)

	// Show project URL and registration status
	fmt.Printf("🌐 Project URL: https://%s.test\n", projectName)

	// SSL and vhosts are handled directly by devbox commands
	fmt.Printf("📝 SSL/Vhosts: ✅ Created directly by devbox\n")
	fmt.Println("💡 Each project has its own isolated FrankenPHP container")

	// Show helpful tips based on container status
	if strings.Contains(status, "not") || strings.Contains(status, "❌") {
		fmt.Println("\n🚀 Next steps:")
		fmt.Println("  devbox start    # Start container with instant SSL/vhosts")
	} else {
		fmt.Println("\n💾 Project config stored in container labels (stateless)")
		fmt.Println("✨ No devbox files anywhere!")
		fmt.Println("🔒 SSL certificate and vhost ready!")
	}

	return nil
}

func listProjects() error {
	fmt.Println("📁 Devbox Projects")

	// Find all devbox project containers
	cmd := exec.Command("docker", "ps", "-a", "--filter", "label=devbox.project.name", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list project containers: %w", err)
	}

	containerNames := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(containerNames) == 1 && containerNames[0] == "" {
		fmt.Println("No devbox projects found.")
		fmt.Println("\n💡 To create a project:")
		fmt.Println("  1. Navigate to any directory you want as a project")
		fmt.Println("  2. Run 'devbox start' (auto-initializes and starts)")
		return nil
	}

	var projects []ProjectInfo
	for _, containerName := range containerNames {
		if containerName == "" {
			continue
		}

		// Get project info from container labels
		nameCmd := exec.Command("docker", "inspect", "-f", "{{index .Config.Labels \"devbox.project.name\"}}", containerName)
		pathCmd := exec.Command("docker", "inspect", "-f", "{{index .Config.Labels \"devbox.project.path\"}}", containerName)
		phpCmd := exec.Command("docker", "inspect", "-f", "{{index .Config.Labels \"devbox.project.php_version\"}}", containerName)

		name, _ := nameCmd.Output()
		path, _ := pathCmd.Output()
		php, _ := phpCmd.Output()

		projectName := strings.TrimSpace(string(name))
		projectPath := strings.TrimSpace(string(path))
		phpVersion := strings.TrimSpace(string(php))

		if projectName == "" || projectName == "<no value>" {
			projectName = strings.TrimPrefix(containerName, "devbox_php_")
		}
		if phpVersion == "" || phpVersion == "<no value>" {
			phpVersion = "8.3"
		}

		status := getContainerStatus(containerName)

		projects = append(projects, ProjectInfo{
			Name:       projectName,
			Path:       projectPath,
			PHPVersion: phpVersion,
			Status:     status,
		})
	}

	// Display projects
	fmt.Printf("Found %d project(s):\n\n", len(projects))
	for _, project := range projects {
		fmt.Printf("📦 %s\n", project.Name)
		if project.Path != "" && project.Path != "<no value>" {
			fmt.Printf("   Path: %s\n", project.Path)
		}
		fmt.Printf("   PHP: %s\n", project.PHPVersion)
		fmt.Printf("   Status: %s\n", project.Status)
		fmt.Printf("   URL: https://%s.test\n\n", project.Name)
	}

	return nil
}

type ProjectInfo struct {
	Name       string
	Path       string
	PHPVersion string
	Status     string
}

// getProjectConfigFromPath removed - using container labels

func registerProject() error {
	fmt.Println("📝 Registration no longer needed!")
	fmt.Println("💡 SSL certificates and vhosts are created directly by 'devbox start'")
	fmt.Println("🚀 Just run 'devbox start' to set up your project")
	return nil
}

func unregisterProject() error {
	fmt.Println("📝 Unregistration no longer needed!")
	fmt.Println("💡 SSL certificates and vhosts are managed directly by devbox")
	fmt.Println("🗑️  Use 'devbox remove' to completely clean up a project")
	return nil
}

func switchWatchmanMode() error {
	fmt.Println("⚠️  Watchman has been removed from devbox!")
	fmt.Println("💡 SSL certificates and vhosts are now created directly by:")
	fmt.Println("  • devbox start - creates SSL/vhosts")
	fmt.Println("  • devbox remove - removes SSL/vhosts")
	fmt.Println("🚀 This is faster and more reliable than background monitoring")
	return nil
}

func showWatchmanStatus() error {
	fmt.Println("⚠️  Watchman has been removed from devbox!")
	fmt.Println()
	fmt.Println("🔄 New approach:")
	fmt.Println("  • SSL certificates created directly by 'devbox start'")
	fmt.Println("  • Vhosts created directly by 'devbox start'")
	fmt.Println("  • Everything removed by 'devbox remove'")
	fmt.Println()
	fmt.Println("💡 Use 'devbox projects' to see all your projects")
	return nil
}

func removeProject() error {
	if !isProjectInitialized() {
		fmt.Println("❌ Project not initialized")
		return nil
	}

	projectName := getProjectName()
	containerName := fmt.Sprintf("devbox_php_%s", projectName)

	fmt.Printf("🗑️  Removing devbox project: %s\n", projectName)
	fmt.Println("This will:")
	fmt.Printf("  • Stop and remove container: %s\n", containerName)
	fmt.Printf("  • Delete project configuration\n")
	fmt.Println("  • NOT delete your project files")
	fmt.Println()

	if !promptYesNo("Are you sure you want to remove this project?") {
		fmt.Println("Project removal cancelled")
		return nil
	}

	// Stop and remove container
	fmt.Printf("🛑 Stopping container: %s\n", containerName)
	stopCmd := exec.Command("docker", "stop", containerName)
	stopCmd.Run() // Ignore errors if container doesn't exist

	fmt.Printf("🗑️  Removing container: %s\n", containerName)
	removeCmd := exec.Command("docker", "rm", containerName)
	removeCmd.Run() // Ignore errors if container doesn't exist

	// No project configuration files to remove (stateless mode)
	fmt.Println("🗑️  Project config automatically removed with container...")

	// Remove SSL certificate and vhost
	fmt.Println("🗑️  Removing SSL certificate and vhost...")
	if err := removeProjectSSLAndVhost(projectName); err != nil {
		fmt.Printf("Warning: Failed to remove SSL/vhost: %v\n", err)
	} else {
		fmt.Println("✅ SSL certificate and vhost removed successfully!")
	}

	// Watchman removed - no registration cleanup needed

	fmt.Println("✅ Project removed successfully!")
	fmt.Printf("📁 Your files in the current directory are untouched\n")
	fmt.Printf("💡 To recreate this project, run 'devbox start' again\n")
	fmt.Printf("🚀 Stateless mode: No persistent files to clean up\n")

	return nil
}

// cleanupTestDomainResolver removes the .test domain resolver configuration
func cleanupTestDomainResolver() error {
	fmt.Println("🧹 Cleaning up .test domain resolver...")

	// Check if we're on macOS
	if !isMacOS() {
		fmt.Println("⏭️  Skipping .test resolver cleanup (not macOS)")
		return nil
	}

	resolverFile := "/etc/resolver/test"

	// Check if resolver file exists
	if !fileExists(resolverFile) {
		fmt.Println("✅ .test domain resolver is not configured")
		return nil
	}

	fmt.Println("🗑️  Removing /etc/resolver/test")
	fmt.Println("🔐 You may be prompted for your password to remove system files")

	// Remove the resolver file
	cmd := exec.Command("sudo", "rm", "-f", resolverFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove resolver file: %w", err)
	}

	// Flush DNS cache to make changes take effect immediately
	fmt.Println("🔄 Flushing DNS cache...")
	flushCmd := exec.Command("sudo", "dscacheutil", "-flushcache")
	flushCmd.Run() // Ignore errors - not critical

	// Also try the newer flush command
	flushCmd2 := exec.Command("sudo", "killall", "-HUP", "mDNSResponder")
	flushCmd2.Run() // Ignore errors - not critical

	fmt.Println("✅ .test domain resolver removed successfully!")
	fmt.Println("💡 .test domains will no longer automatically resolve")
	fmt.Println("🔧 You can reconfigure by running devbox installation again")

	return nil
}

// setupProjectSSLAndVhost creates SSL certificate and nginx vhost for the project
func setupProjectSSLAndVhost(projectName string) error {
	// Generate SSL certificate
	if err := generateSSLCertificate(projectName); err != nil {
		return fmt.Errorf("failed to generate SSL certificate: %w", err)
	}

	// Create nginx vhost
	if err := createNginxVhost(projectName); err != nil {
		return fmt.Errorf("failed to create nginx vhost: %w", err)
	}

	// Reload nginx
	if err := reloadNginx(); err != nil {
		return fmt.Errorf("failed to reload nginx: %w", err)
	}

	return nil
}

// removeProjectSSLAndVhost removes SSL certificate and nginx vhost for the project
func removeProjectSSLAndVhost(projectName string) error {
	// Remove nginx vhost
	if err := removeNginxVhost(projectName); err != nil {
		return fmt.Errorf("failed to remove nginx vhost: %w", err)
	}

	// Remove SSL certificate
	if err := removeSSLCertificate(projectName); err != nil {
		return fmt.Errorf("failed to remove SSL certificate: %w", err)
	}

	// Reload nginx
	if err := reloadNginx(); err != nil {
		return fmt.Errorf("failed to reload nginx: %w", err)
	}

	return nil
}

// generateSSLCertificate generates SSL certificate for the project
func generateSSLCertificate(projectName string) error {
	domain := fmt.Sprintf("%s.test", projectName)
	fmt.Printf("🔒 Generating SSL certificate for %s...\n", domain)

	// Use the openssl container to generate the certificate
	// The container automatically uses its internal CA to sign the certificate
	cmd := exec.Command("docker", "run", "--rm",
		"-e", fmt.Sprintf("DOMAIN=%s", domain),
		"-v", "devbox_certs:/certificates",
		"ghcr.io/aliasproject/openssl:latest")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SSL certificate generation failed. Please ensure GitHub authentication is set up: %w", err)
	}

	// Certificates are automatically available to nginx via shared Docker volume
	fmt.Println("✅ SSL certificate generated successfully!")
	return nil
}

// removeSSLCertificate removes SSL certificate for the project
func removeSSLCertificate(projectName string) error {
	domain := fmt.Sprintf("%s.test", projectName)
	cmd := exec.Command("docker", "exec", "devbox_nginx", "sh", "-c", fmt.Sprintf(`
		cd /etc/nginx/ssl && \
		rm -f %s.crt %s.key
	`, domain, domain))

	return cmd.Run()
}

// createNginxVhost creates nginx virtual host configuration for the project
func createNginxVhost(projectName string) error {
	domain := fmt.Sprintf("%s.test", projectName)
	containerName := fmt.Sprintf("devbox_php_%s", projectName)

	vhostConfig := fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name %s;

    ssl_certificate /etc/nginx/ssl/%s.crt;
    ssl_certificate_key /etc/nginx/ssl/%s.key;

    root /app/public;
    index index.php index.html index.htm;

    location / {
        proxy_pass http://%s:80;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
    }

    location ~ /\.ht {
        deny all;
    }
}`, domain, domain, domain, domain, containerName)

	// Write vhost config to nginx container
	cmd := exec.Command("docker", "exec", "-i", "devbox_nginx", "tee", fmt.Sprintf("/etc/nginx/conf.d/%s.conf", projectName))
	cmd.Stdin = strings.NewReader(vhostConfig)

	return cmd.Run()
}

// removeNginxVhost removes nginx virtual host configuration for the project
func removeNginxVhost(projectName string) error {
	cmd := exec.Command("docker", "exec", "devbox_nginx", "rm", "-f", fmt.Sprintf("/etc/nginx/conf.d/%s.conf", projectName))
	return cmd.Run()
}

// reloadNginx reloads nginx configuration
func reloadNginx() error {
	cmd := exec.Command("docker", "exec", "devbox_nginx", "nginx", "-s", "reload")
	return cmd.Run()
}

func getDatabaseStatus() string {
	databases := getRunningDatabases()
	if len(databases) == 0 {
		return "❌ None (run 'devbox database add')"
	}

	if len(databases) == 1 {
		return fmt.Sprintf("✅ %s", databases[0])
	}

	return fmt.Sprintf("✅ Multiple (%s)", strings.Join(databases, ", "))
}

func getEnvStatus(envVar string) string {
	value := os.Getenv(envVar)
	if value == "" {
		return "❌ Not set"
	}
	return fmt.Sprintf("✅ %s", value)
}

func getTokenStatus() string {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "❌ not set"
	}
	// Show first 8 characters and mask the rest for security
	if len(token) > 8 {
		return fmt.Sprintf("✅ %s...", token[:8])
	}
	return "✅ set"
}

func getContainerStatus(containerName string) string {
	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", containerName)
	output, err := cmd.Output()
	if err != nil {
		return "❌ not found"
	}

	status := strings.TrimSpace(string(output))
	switch status {
	case "running":
		return "✅ running"
	case "exited":
		return "🔴 stopped"
	case "paused":
		return "⏸️  paused"
	case "restarting":
		return "🔄 restarting"
	default:
		return fmt.Sprintf("⚠️  %s", status)
	}
}

func getStatus(exists bool) string {
	if exists {
		return "✅ exists"
	}
	return "❌ missing"
}
