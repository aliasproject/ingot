package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	version      = "0.6.0"
	githubRepo   = "aliasproject/devbox"
	devboxBinary = "/usr/local/bin/devbox"
)

var (
	homeDir     = os.Getenv("HOME")
	devboxDir   = filepath.Join(homeDir, ".devbox")
	composeFile = filepath.Join(devboxDir, "docker-compose.yml")
	envVarsFile = filepath.Join(devboxDir, "env_vars")
)

type release struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID                 int    `json:"id"`
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func main() {
	// Load environment variables from file
	if isInstalled() {
		if err := loadEnvVars(); err != nil {
			fmt.Printf("Warning: Failed to load environment variables: %v\n", err)
		}
	}

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

	// Handle install command (for when devbox is already installed)
	if len(args) > 0 && args[0] == "install" {
		fmt.Println("Devbox is already installed!")
		fmt.Printf("Version: v%s\n", version)
		fmt.Println()

		// Show installation status
		fmt.Println("Installation Status:")
		fmt.Printf("  Config directory: %s (%s)\n", devboxDir, getStatus(dirExists(devboxDir)))
		fmt.Printf("  Binary location: %s (%s)\n", devboxBinary, getStatus(fileExists(devboxBinary)))
		fmt.Printf("  Env vars file: %s (%s)\n", envVarsFile, getStatus(fileExists(envVarsFile)))
		fmt.Printf("  Docker compose: %s (%s)\n", composeFile, getStatus(fileExists(composeFile)))

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
		// Default behavior - show container status
		if !isInsideProjectDir() {
			fmt.Println("Error: You must be inside a project directory to run devbox.")
			fmt.Println("Navigate to your project root directory and try again.")
			os.Exit(1)
		}

		dockerArgs := []string{"compose", "-f", composeFile, "ps"}
		if err := runDockerComposeCommand(dockerArgs); err != nil {
			fmt.Printf("Failed to show container status: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Exit if not inside project directory
	if !isInsideProjectDir() {
		fmt.Printf("Please try again from inside projects directory: %s\n", os.Getenv("DEVBOX_PROJECT_ROOT"))
		os.Exit(1)
	}

	// Get relative path within project
	relPath, err := getRelativeProjectPath()
	if err != nil {
		fmt.Printf("Error getting relative path: %v\n", err)
		os.Exit(1)
	}

	containerPath := "/home/alias" + relPath

	// Check if directory exists in container
	dirExists := directoryExistsInContainer(containerPath)

	if dirExists {
		// Execute commands in container with shortcuts
		if err := executeInContainer(args); err != nil {
			fmt.Printf("Command failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Directory doesn't exist in container, run docker compose command directly
		dockerArgs := []string{"compose", "-f", composeFile}
		dockerArgs = append(dockerArgs, args...)
		if err := runDockerComposeCommand(dockerArgs); err != nil {
			fmt.Printf("Docker compose command failed: %v\n", err)
			os.Exit(1)
		}
	}
}

func isInstalled() bool {
	_, err1 := os.Stat(devboxDir)
	_, err2 := os.Stat(composeFile)
	_, err3 := os.Stat(envVarsFile)
	return err1 == nil && err2 == nil && err3 == nil
}

func loadEnvVars() error {
	file, err := os.Open(envVarsFile)
	if err != nil {
		return fmt.Errorf("failed to open env vars file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			os.Setenv(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}

	return scanner.Err()
}

func isInsideProjectDir() bool {
	projectRoot := os.Getenv("DEVBOX_PROJECT_ROOT")
	if projectRoot == "" {
		return false
	}

	pwd, err := os.Getwd()
	if err != nil {
		return false
	}

	return strings.HasPrefix(pwd, projectRoot)
}

func getRelativeProjectPath() (string, error) {
	projectRoot := os.Getenv("DEVBOX_PROJECT_ROOT")
	if projectRoot == "" {
		return "", fmt.Errorf("DEVBOX_PROJECT_ROOT not set")
	}

	pwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return strings.TrimPrefix(pwd, projectRoot), nil
}

func directoryExistsInContainer(path string) bool {
	// Use docker compose exec to check if directory exists in the PHP container
	cmdStr := fmt.Sprintf("test -d %s && echo true || echo false", path)
	cmd := exec.Command("docker", "compose", "-f", composeFile, "exec", "-T", "php", "sh", "-c", cmdStr)

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

	containerPath := "/home/alias" + relPath

	// Check if directory exists in container
	if !directoryExistsInContainer(containerPath) {
		return fmt.Errorf("directory %s does not exist in container", containerPath)
	}

	// Handle specific shortcuts with shell commands
	var dockerArgs []string
	switch args[0] {
	case "artisan":
		dockerArgs = []string{"compose", "-f", composeFile, "exec", "-it", "-w", containerPath, "php", "sh", "-c", "php artisan " + strings.Join(args[1:], " ")}
	case "composer":
		dockerArgs = []string{"compose", "-f", composeFile, "exec", "-it", "-w", containerPath, "php", "sh", "-c", "composer " + strings.Join(args[1:], " ")}
	case "npm":
		dockerArgs = []string{"compose", "-f", composeFile, "exec", "-it", "-w", containerPath, "php", "sh", "-c", "npm " + strings.Join(args[1:], " ")}
	default:
		// Build docker compose exec command for other commands
		dockerArgs = []string{"compose", "-f", composeFile, "exec", "-it", "-w", containerPath, "php"}
		dockerArgs = append(dockerArgs, args...)
	}

	return runDockerCommand(dockerArgs)
}

func runDockerCommand(args []string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Load environment variables if devbox is installed
	if isInstalled() {
		if err := loadEnvVars(); err != nil {
			fmt.Printf("Warning: Failed to load environment variables: %v\n", err)
		}
	}

	// Set environment variables for the command
	cmd.Env = os.Environ()

	return cmd.Run()
}

func runDockerComposeCommand(args []string) error {
	// Check if this command might need to pull images
	needsImagePull := false
	for _, arg := range args {
		if arg == "up" || arg == "pull" || arg == "create" {
			needsImagePull = true
			break
		}
	}

	// If we might need to pull images, ensure GitHub authentication
	if needsImagePull && isInstalled() {
		if err := ensureGitHubAuth(); err != nil {
			fmt.Printf("Warning: GitHub authentication failed: %v\n", err)
			fmt.Println("Container pulls may fail. Continuing anyway...")
		}
	}

	// Ensure we have all required environment variables set
	envVars := []string{
		"DEVBOX_PROJECT_ROOT",
		"PHP_VERSION",
		"MYSQL_SERVER",
		"HOME",
	}

	for _, envVar := range envVars {
		if os.Getenv(envVar) == "" {
			// Try to load from env vars file if available
			if isInstalled() {
				if err := loadEnvVars(); err != nil {
					return fmt.Errorf("failed to load environment variables: %w", err)
				}
			}
			break
		}
	}

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
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
		fmt.Printf("Debug: Using authenticated request for %s\n", githubRepo)
	} else {
		fmt.Printf("Debug: Using unauthenticated request for %s\n", githubRepo)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	fmt.Printf("Debug: GitHub API response status: %d\n", resp.StatusCode)

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

	fmt.Printf("Debug: Found latest release: %s (current: v%s)\n", rel.TagName, version)
	return rel.TagName != "v"+version, rel.TagName, nil
}

// getGitHubToken retrieves GitHub token from environment or stored config
func getGitHubToken() (string, error) {
	// First check environment variable
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// Load from env_vars file if not in environment
	if err := loadEnvVars(); err == nil {
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			return token, nil
		}
	}

	// Try to get token from GitHub CLI if available
	if token, err := getGitHubCLIToken(); err == nil && token != "" {
		fmt.Println("✅ Using GitHub token from GitHub CLI")

		// Store token in env_vars file for future use
		if err := writeEnvVar("GITHUB_TOKEN", token); err != nil {
			fmt.Printf("Warning: Failed to save token for future use: %v\n", err)
		} else {
			// Set in current environment
			os.Setenv("GITHUB_TOKEN", token)
		}

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

	// Store token in env_vars file for future use
	if err := writeEnvVar("GITHUB_TOKEN", token); err != nil {
		fmt.Printf("Warning: Failed to save token for future use: %v\n", err)
	} else {
		// Set in current environment
		os.Setenv("GITHUB_TOKEN", token)
		fmt.Println("✅ GitHub token saved successfully.")
	}

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

	// Debug: Show the URL we're trying to open
	fmt.Printf("🔍 Debug: Basic URL: %s\n", deviceResp.VerificationURI)
	fmt.Printf("🔍 Debug: Complete URL: %s\n", deviceResp.VerificationURIComplete)

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
		return "", fmt.Errorf(tokenResp.Error)
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

	// Try to load from env vars file
	if isInstalled() {
		if err := loadEnvVars(); err == nil {
			if token := os.Getenv("GITHUB_TOKEN"); token != "" {
				return nil
			}
		}
	}

	// No token found, try to get one
	fmt.Println("🔑 GitHub authentication required for container registry access...")
	token, err := getGitHubToken()
	if err != nil {
		return fmt.Errorf("failed to get GitHub token: %w", err)
	}

	// Login to GitHub Container Registry
	fmt.Println("🔐 Logging into GitHub Container Registry...")
	loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", "token", "--password-stdin")
	loginCmd.Stdin = strings.NewReader(token)
	loginCmd.Stdout = os.Stdout
	loginCmd.Stderr = os.Stderr

	if err := loginCmd.Run(); err != nil {
		return fmt.Errorf("failed to login to GitHub Container Registry: %w", err)
	}

	fmt.Println("✅ Successfully authenticated with GitHub Container Registry!")
	return nil
}

func openURL(url string) error {
	fmt.Printf("🔍 Debug: Attempting to open URL on macOS\n")

	// For macOS, try multiple approaches
	if _, err := exec.LookPath("open"); err == nil {
		// Try method 1: open with URL flag (forces browser)
		fmt.Printf("🔍 Debug: Trying 'open -u' command\n")
		cmd := exec.Command("open", "-u", url)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err == nil {
			return nil
		}
		fmt.Printf("🔍 Debug: 'open -u' failed, trying regular 'open'\n")

		// Try method 2: regular open
		cmd = exec.Command("open", url)
		cmd.Stderr = os.Stderr
		cmd.Stdout = os.Stdout
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

	// Update token in env_vars file
	if err := updateEnvVar("GITHUB_TOKEN", token); err != nil {
		return fmt.Errorf("failed to update token: %w", err)
	}

	// Set in current environment
	os.Setenv("GITHUB_TOKEN", token)
	fmt.Println("GitHub token updated successfully.")

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

// updateEnvVar updates an existing environment variable in the env_vars file
func updateEnvVar(key, value string) error {
	// Read existing content
	content, err := os.ReadFile(envVarsFile)
	if err != nil {
		// If file doesn't exist, create it with the new variable
		return writeEnvVar(key, value)
	}

	lines := strings.Split(string(content), "\n")
	updated := false

	// Update existing line or add new one
	for i, line := range lines {
		if strings.HasPrefix(line, key+"=") {
			lines[i] = fmt.Sprintf("%s=%s", key, value)
			updated = true
			break
		}
	}

	if !updated {
		lines = append(lines, fmt.Sprintf("%s=%s", key, value))
	}

	// Write back to file
	return os.WriteFile(envVarsFile, []byte(strings.Join(lines, "\n")), 0644)
}

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

	// Get project directory
	fmt.Println("📂 Configuring project directory...")
	projectDir, err := getProjectDirectory()
	if err != nil {
		return fmt.Errorf("failed to get project directory: %w", err)
	}

	// Write project root to env vars
	if err := writeEnvVar("DEVBOX_PROJECT_ROOT", projectDir); err != nil {
		return fmt.Errorf("failed to write project root: %w", err)
	}

	// Set environment variable in current process for immediate use
	os.Setenv("DEVBOX_PROJECT_ROOT", projectDir)

	// Get PHP version
	fmt.Println()
	phpVersion := selectOption("Select PHP Version", []string{"8.2", "8.1", "8.0"})
	if err := writeEnvVar("PHP_VERSION", phpVersion); err != nil {
		return fmt.Errorf("failed to write PHP version: %w", err)
	}

	// Set environment variable in current process for immediate use
	os.Setenv("PHP_VERSION", phpVersion)

	// Get MySQL version
	fmt.Println()
	mysqlVersion := selectOption("Select MySQL Version", []string{"8.0", "5.7"})
	if err := writeEnvVar("MYSQL_SERVER", mysqlVersion); err != nil {
		return fmt.Errorf("failed to write MySQL version: %w", err)
	}

	// Set environment variable in current process for immediate use
	os.Setenv("MYSQL_SERVER", mysqlVersion)

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
		// Login to GitHub Container Registry
		fmt.Println("🔐 Logging into GitHub Container Registry...")
		loginCmd := exec.Command("docker", "login", "ghcr.io", "-u", "token", "--password-stdin")
		loginCmd.Stdin = strings.NewReader(token)
		loginCmd.Stdout = os.Stdout
		loginCmd.Stderr = os.Stderr

		if err := loginCmd.Run(); err != nil {
			fmt.Printf("❌ Warning: Failed to login to GitHub Container Registry: %v\n", err)
			fmt.Println("💡 Container pulls may fail. You can try authentication later with 'devbox update'.")
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

	// Create docker-compose.yml file with Watchman container
	fmt.Println()
	fmt.Println("🐳 Creating Docker configuration...")
	composeContent := `services:
    # ------------------------------------------------------------
    # Dnsmasq (DNS Server)
    # ------------------------------------------------------------
    dns:
        container_name: devbox_dns
        image: ghcr.io/aliasproject/dns:latest
        restart: unless-stopped
        ports:
            - "5354:53/udp"
        networks:
            internal:
                ipv4_address: 192.168.237.4
        environment:
            - COMMANDS=--address=/test/127.0.0.1 --server=1.1.1.1 --server=8.8.8.8 --log-facility=-
        dns:
            - 1.1.1.1
            - 8.8.8.8

    # ------------------------------------------------------------
    # PHP
    # ------------------------------------------------------------
    php:
        container_name: devbox_php
        image: ghcr.io/aliasproject/php:${PHP_VERSION:-8.2}-fpm
        restart: unless-stopped
        volumes:
            - ${DEVBOX_PROJECT_ROOT:-./}:/home/alias
            - /var/run/docker.sock:/var/run/docker.sock
        dns:
            - 192.168.237.4
        networks:
            - internal
        depends_on:
            - dns

    # ------------------------------------------------------------
    # Nginx (Web Server)
    # ------------------------------------------------------------
    nginx:
        container_name: devbox_nginx
        image: ghcr.io/aliasproject/nginx:latest
        restart: unless-stopped
        volumes:
            - certs:/etc/nginx/ssl
            - vhosts:/etc/nginx/conf.d
            - ${DEVBOX_PROJECT_ROOT:-./}:/home/alias:ro
        ports:
            - 80:80
            - 443:443
        networks:
            - dev-network
            - internal
        depends_on:
            - dns
            - php

    # ------------------------------------------------------------
    # Watchman (Vhost Gen)
    # ------------------------------------------------------------
    watchman:
        container_name: devbox_watchman
        image: ghcr.io/aliasproject/watchman:latest
        restart: unless-stopped
        environment:
            - DOCKER_USER=mike
            - DOCKER_TOKEN=test
        volumes:
            - certs:/certs
            - vhosts:/vhosts
            - .:/public
            - ${DEVBOX_PROJECT_ROOT:-./}:/home/alias:ro
            - /var/run/docker.sock:/var/run/docker.sock
        networks:
            - internal
        depends_on:
            - nginx
        healthcheck:
            test: ash -c "[ -f /certs/AliasProjectCASelfSigned.pem ]"

    # ------------------------------------------------------------
    # MySQL (Database)
    # ------------------------------------------------------------
    mysql:
        container_name: devbox_mysql
        image: mysql:${MYSQL_SERVER:-8.0}
        platform: linux/amd64
        restart: unless-stopped
        environment:
            - MYSQL_ROOT_PASSWORD=
            - MYSQL_ALLOW_EMPTY_PASSWORD=yes
        ports:
            - 3306:3306
        networks:
            - dev-network
            - internal
        volumes:
            - mysql-${MYSQL_SERVER:-8.0}:/var/lib/mysql
        depends_on:
            - dns
            - php
            - nginx

    # ------------------------------------------------------------
    # Mailhog (Mail)
    # ------------------------------------------------------------
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
    # ------------------------------------------------------------
    # NGINX
    # ------------------------------------------------------------
    certs:
    vhosts:

    # ------------------------------------------------------------
    # MySQL
    # ------------------------------------------------------------
    mysql-8.0:
    mysql-5.7:

networks:
    internal:
        driver: bridge
        ipam:
            config:
                - subnet: 192.168.237.0/24
    dev-network:
        driver: bridge`

	if err := os.WriteFile(composeFile, []byte(composeContent), 0644); err != nil {
		return fmt.Errorf("failed to create docker-compose.yml: %w", err)
	}

	// Create certs directory - this will be mapped to the Docker volume
	certsDir := filepath.Join(devboxDir, "certs")
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// Set HOME environment variable to ensure it's available for docker-compose
	if homeDir != "" {
		os.Setenv("HOME", homeDir)
	}

	// Start containers
	fmt.Println()
	fmt.Println("🚀 Starting Docker containers...")

	// Debug: Show environment variables that will be used
	fmt.Printf("Debug: DEVBOX_PROJECT_ROOT=%s\n", os.Getenv("DEVBOX_PROJECT_ROOT"))
	fmt.Printf("Debug: PHP_VERSION=%s\n", os.Getenv("PHP_VERSION"))
	fmt.Printf("Debug: MYSQL_SERVER=%s\n", os.Getenv("MYSQL_SERVER"))
	fmt.Printf("Debug: HOME=%s\n", os.Getenv("HOME"))

	if err := startContainers(); err != nil {
		return fmt.Errorf("failed to start containers: %w", err)
	}

	// Wait for Watchman to be healthy and generate SSL certificates
	fmt.Println()
	fmt.Println("🔍 Waiting for Watchman to scan project and generate SSL certificates...")
	if err := waitForWatchman(); err != nil {
		return fmt.Errorf("failed while waiting for Watchman: %w", err)
	}

	// Setup SSL certificates and prompt for CA acceptance
	fmt.Println()
	if err := setupSSL(); err != nil {
		return fmt.Errorf("failed to setup SSL: %w", err)
	}

	fmt.Println()
	fmt.Println("✅ Devbox installed successfully!")
	fmt.Println("🌟 You can now run 'devbox' from anywhere in your project directory.")
	fmt.Println("🔒 SSL certificates have been generated for your project domains.")
	fmt.Println()
	fmt.Printf("📁 Project directory: %s\n", projectDir)
	fmt.Printf("🐘 PHP version: %s\n", phpVersion)
	fmt.Printf("🐬 MySQL version: %s\n", mysqlVersion)

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

func writeEnvVar(key, value string) error {
	// Ensure the directory exists
	if err := os.MkdirAll(filepath.Dir(envVarsFile), 0755); err != nil {
		return fmt.Errorf("failed to create directory for env vars file: %w", err)
	}

	line := fmt.Sprintf("%s=%s\n", key, value)
	file, err := os.OpenFile(envVarsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(line)
	return err
}

func getProjectDirectory() (string, error) {
	pwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	fmt.Printf("Current directory: %s\n", pwd)
	if promptYesNo("Is this your project root directory?") {
		return pwd, nil
	}

	fmt.Print("Enter project root directory path: ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		path := strings.TrimSpace(scanner.Text())
		if path == "" {
			return pwd, nil
		}
		return path, nil
	}

	return pwd, nil
}

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
	return runDockerComposeCommand([]string{"compose", "-f", composeFile, "up", "-d"})
}

func waitForWatchman() error {
	fmt.Println("Waiting for Watchman container to start...")

	// Wait up to 5 minutes for Watchman to become healthy
	timeout := time.After(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for Watchman to become healthy")
		case <-ticker.C:
			if isWatchmanHealthy() {
				fmt.Println("Watchman is healthy and ready.")

				// Give Watchman additional time to scan project and generate certificates
				fmt.Println("Allowing time for SSL certificate generation...")
				time.Sleep(30 * time.Second)

				return nil
			}
			fmt.Println("Watchman not ready yet, checking again...")
		}
	}
}

func setupSSL() error {
	fmt.Println("🔒 Setting up SSL certificates...")

	// Copy CA certificate from Watchman container
	caCertPath := filepath.Join(devboxDir, "certs", "AliasProjectCASelfSigned.pem")
	fmt.Println("📋 Copying certificate from Watchman container...")

	cmd := exec.Command("docker", "compose", "-f", composeFile, "cp", "devbox_watchman:/certs/AliasProjectCASelfSigned.pem", caCertPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Warning: Failed to copy certificate using docker compose cp: %v\n", err)
		fmt.Println("Attempting alternative method using docker cp...")

		// Try alternative method with docker cp directly
		altCmd := exec.Command("docker", "cp", "devbox_watchman:/certs/AliasProjectCASelfSigned.pem", caCertPath)
		altCmd.Stdout = os.Stdout
		altCmd.Stderr = os.Stderr

		if err := altCmd.Run(); err != nil {
			return fmt.Errorf("failed to copy CA certificate from Watchman container: %w", err)
		}
	}

	// Verify the certificate was copied successfully
	if !fileExists(caCertPath) {
		return fmt.Errorf("CA certificate not found at %s after copying from container", caCertPath)
	}

	fmt.Println("✅ Certificate copied successfully!")

	// Prompt user to install CA certificate
	fmt.Println("\nTo enable HTTPS for your development environment, you need to install the")
	fmt.Println("Alias Project Certificate Authority (CA) certificate on your system.")
	fmt.Println("This allows your browser to trust the SSL certificates generated for your project.")

	if !promptYesNo("Would you like to install the CA certificate now?") {
		fmt.Println("Skipping CA certificate installation. You can install it later manually.")
		fmt.Printf("CA certificate location: %s\n", caCertPath)
		return nil
	}

	// Install CA certificate (macOS)
	fmt.Println()
	fmt.Println("🔐 Installing CA certificate to system keychain...")
	fmt.Println("💡 You may be prompted for your password to authorize this change.")

	sudoCmd := exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain", caCertPath)
	sudoCmd.Stdout = os.Stdout
	sudoCmd.Stderr = os.Stderr
	sudoCmd.Stdin = os.Stdin

	if err := sudoCmd.Run(); err != nil {
		fmt.Printf("Warning: Failed to install CA certificate automatically: %v\n", err)
		fmt.Printf("You can install it manually: sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s\n", caCertPath)
	} else {
		fmt.Println("✅ CA certificate installed successfully!")
	}

	return nil
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

func isWatchmanHealthy() bool {
	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Health.Status}}", "devbox_watchman")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "healthy"
}

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
	fmt.Println("USAGE:")
	fmt.Println("  devbox [COMMAND]")
	fmt.Println()
	fmt.Println("COMMANDS:")
	fmt.Println("  help, -h, --help     Show this help message")
	fmt.Println("  -v, --version        Show version information")
	fmt.Println("  update               Update devbox to the latest version")
	fmt.Println("  install              Show installation status")
	fmt.Println("  status               Show complete system status")
	fmt.Println()
	fmt.Println("DOCKER COMPOSE COMMANDS:")
	fmt.Println("  up                   Start containers")
	fmt.Println("  down                 Stop containers")
	fmt.Println("  ps                   Show container status")
	fmt.Println("  logs                 Show container logs")
	fmt.Println("  pull                 Pull latest container images")
	fmt.Println("  restart              Restart containers")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  devbox status        # Show complete status")
	fmt.Println("  devbox up            # Start all containers")
	fmt.Println("  devbox ps            # Show running containers")
	fmt.Println("  devbox logs php      # Show PHP container logs")
	fmt.Println("  devbox down          # Stop all containers")
	fmt.Println()
	fmt.Println("For more information, visit: https://github.com/aliasproject/devbox")
}

func showStatus() error {
	fmt.Printf("🔧 Devbox v%s - System Status\n\n", version)

	// Installation Status
	fmt.Println("📦 Installation Status:")
	fmt.Printf("  Config directory: %s (%s)\n", devboxDir, getStatus(dirExists(devboxDir)))
	fmt.Printf("  Binary location: %s (%s)\n", devboxBinary, getStatus(fileExists(devboxBinary)))
	fmt.Printf("  Env vars file: %s (%s)\n", envVarsFile, getStatus(fileExists(envVarsFile)))
	fmt.Printf("  Docker compose: %s (%s)\n", composeFile, getStatus(fileExists(composeFile)))

	// Environment Variables Status
	fmt.Println("\n🔧 Configuration:")
	if isInstalled() {
		loadEnvVars() // Load variables to check them
		fmt.Printf("  Project root: %s\n", getEnvStatus("DEVBOX_PROJECT_ROOT"))
		fmt.Printf("  PHP version: %s\n", getEnvStatus("PHP_VERSION"))
		fmt.Printf("  MySQL version: %s\n", getEnvStatus("MYSQL_SERVER"))
		fmt.Printf("  GitHub token: %s\n", getTokenStatus())
	} else {
		fmt.Println("  ❌ Devbox not installed")
	}

	// Docker Status
	fmt.Println("\n🐳 Docker Status:")
	if !isDockerInstalled() {
		fmt.Println("  ❌ Docker not installed or not running")
		return nil
	}
	fmt.Println("  ✅ Docker is running")

	// Container Status
	if !isInstalled() {
		fmt.Println("  ❌ Devbox not installed - no containers to check")
		return nil
	}

	if !isInsideProjectDir() {
		fmt.Println("  ⚠️  Not in project directory - cannot check container status")
		return nil
	}

	fmt.Println("\n📊 Container Status:")

	// Check individual containers
	containers := []string{"devbox_dns", "devbox_php", "devbox_nginx", "devbox_watchman", "devbox_mysql", "devbox_mail"}
	for _, container := range containers {
		status := getContainerStatus(container)
		fmt.Printf("  %s: %s\n", container, status)
	}

	// Show port mappings
	fmt.Println("\n🌐 Port Mappings:")
	fmt.Println("  HTTP: http://localhost:80")
	fmt.Println("  HTTPS: https://localhost:443")
	fmt.Println("  MySQL: localhost:3306")
	fmt.Println("  Mailhog: http://localhost:8025")
	fmt.Println("  DNS: localhost:5354")

	return nil
}

func getEnvStatus(envVar string) string {
	value := os.Getenv(envVar)
	if value == "" {
		return "❌ not set"
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
