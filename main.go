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

	// Handle network-help command
	if len(args) > 0 && args[0] == "network-help" {
		showNetworkHelp()
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

	// Handle watchman mode command
	if len(args) > 0 && args[0] == "watchman" {
		if len(args) > 1 && args[1] == "mode" {
			if err := switchWatchmanMode(); err != nil {
				fmt.Printf("Watchman mode switch failed: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if len(args) > 1 && args[1] == "status" {
			if err := showWatchmanStatus(); err != nil {
				fmt.Printf("Failed to show watchman status: %v\n", err)
				os.Exit(1)
			}
			return
		}
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
		// Default behavior - show project status if in project, otherwise show help
		if isProjectInitialized() {
			if err := showProjectStatus(); err != nil {
				fmt.Printf("Failed to show project status: %v\n", err)
				os.Exit(1)
			}
			return
		} else {
			fmt.Println("💡 No devbox project found in current directory")
			fmt.Println("Run 'devbox init' to initialize a project, or 'devbox help' for more options")
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
		// Run global docker compose command if not in a project
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

	// Note: PHP version is now configured per-project
	fmt.Println("📦 PHP containers will be created per-project with configurable versions")

	// Get MySQL version
	fmt.Println()
	mysqlVersion := selectOption("Select MySQL Version", []string{"8.0", "5.7"})
	if err := writeEnvVar("MYSQL_SERVER", mysqlVersion); err != nil {
		return fmt.Errorf("failed to write MySQL version: %w", err)
	}

	// Set environment variable in current process for immediate use
	os.Setenv("MYSQL_SERVER", mysqlVersion)

	// Get Watchman mode
	fmt.Println()
	fmt.Println("Watchman Configuration:")
	fmt.Println("  Auto-watch - Automatically detect and configure new projects in a directory")
	fmt.Println("  Manual - Manually register projects using 'devbox register' command")
	watchmanMode := selectOption("Select Watchman Mode", []string{"Auto-watch", "Manual"})
	if err := writeEnvVar("WATCHMAN_MODE", watchmanMode); err != nil {
		return fmt.Errorf("failed to write watchman mode: %w", err)
	}

	// Set environment variable in current process for immediate use
	os.Setenv("WATCHMAN_MODE", watchmanMode)

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

	// Get host IP address for network-wide access
	hostIP, err := getHostIP()
	if err != nil {
		fmt.Printf("Warning: Could not determine host IP address: %v\n", err)
		fmt.Println("Using 127.0.0.1 - domains will only work on this machine")
		hostIP = "127.0.0.1"
	} else {
		fmt.Printf("🌐 Configuring domains to work across network using IP: %s\n", hostIP)
	}

	// Create docker-compose.yml file with Watchman container
	fmt.Println()
	fmt.Println("🐳 Creating Docker configuration...")
	composeContent := fmt.Sprintf(`services:
    # ------------------------------------------------------------
    # Dnsmasq (DNS Server)
    # ------------------------------------------------------------
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
            - devbox-network
            - internal
        depends_on:
            - dns

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
            - WATCHMAN_MODE=${WATCHMAN_MODE:-Auto-watch}
        volumes:
            - certs:/certs
            - vhosts:/vhosts
            - .:/public
            - ${DEVBOX_PROJECT_ROOT:-./}:/home/alias:ro
            - /var/run/docker.sock:/var/run/docker.sock
            - %s/manual-projects:/manual-projects:ro
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
            - devbox-network
            - internal
        volumes:
            - mysql-${MYSQL_SERVER:-8.0}:/var/lib/mysql
        depends_on:
            - dns
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
    devbox-network:
        driver: bridge`, hostIP, devboxDir)

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

	// Start global services
	fmt.Println()
	fmt.Println("🚀 Starting global Docker services...")

	if err := startGlobalServices(); err != nil {
		return fmt.Errorf("failed to start global services: %w", err)
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
	fmt.Println("🐘 PHP version: Per-project configuration")
	fmt.Printf("🐬 MySQL version: %s\n", mysqlVersion)
	fmt.Printf("👁️  Watchman mode: %s\n", watchmanMode)

	// Show network access information
	fmt.Println()
	fmt.Println("🌍 Network Access:")
	fmt.Println("  📍 Local machine: https://yourproject.test")
	if hostIP != "127.0.0.1" {
		fmt.Printf("  🌐 Network devices: Configure DNS to %s:5354 for .test domains\n", hostIP)
		fmt.Printf("  📱 Direct IP access: http://%s or https://%s\n", hostIP, hostIP)
		fmt.Println("  🔧 Requires DNS configuration on other devices for .test domains")
		fmt.Println("  💡 Run 'devbox network-help' for setup instructions")
	}

	fmt.Println()
	fmt.Println("🚀 Next Steps:")
	fmt.Println("  1. Navigate to your project directory")
	fmt.Println("  2. Run 'devbox init' to initialize the project")
	fmt.Println("  3. Run 'devbox start' to start project containers")
	if watchmanMode == "Manual" {
		fmt.Println("  4. Run 'devbox register' to register with Watchman for SSL/vhost")
	} else {
		fmt.Println("  4. Projects in your root directory will be auto-detected by Watchman")
	}

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
	fmt.Println()
	fmt.Println("PROJECT COMMANDS:")
	fmt.Println("  init                 Initialize current directory as a devbox project")
	fmt.Println("  start                Start project containers")
	fmt.Println("  stop                 Stop project containers")
	fmt.Println("  php-version          Change PHP version for current project")
	fmt.Println("  projects             List all devbox projects")
	fmt.Println("  register             Register current project with Watchman (manual mode)")
	fmt.Println("  unregister           Unregister current project from Watchman")
	fmt.Println()
	fmt.Println("WATCHMAN COMMANDS:")
	fmt.Println("  watchman mode        Switch between Auto-watch and Manual modes")
	fmt.Println("  watchman status      Show Watchman status and registered projects")
	fmt.Println()
	fmt.Println("GLOBAL COMMANDS:")
	fmt.Println("  global start         Start global devbox services")
	fmt.Println("  global stop          Stop global devbox services")
	fmt.Println()
	fmt.Println("DOCKER COMPOSE COMMANDS:")
	fmt.Println("  up                   Start containers")
	fmt.Println("  down                 Stop containers")
	fmt.Println("  ps                   Show container status")
	fmt.Println("  logs                 Show container logs")
	fmt.Println("  pull                 Pull latest container images")
	fmt.Println("  restart              Restart containers")
	fmt.Println()
	fmt.Println("🌍 NETWORK ACCESS:")
	fmt.Println("  Local machine:       https://yourproject.test")
	fmt.Println("  Network devices:     Configure DNS for .test domains")
	fmt.Println("  Direct IP access:    Check 'devbox status' for your IP")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  devbox init          # Initialize project in current directory")
	fmt.Println("  devbox start         # Start project containers")
	fmt.Println("  devbox php-version   # Change PHP version for project")
	fmt.Println("  devbox register      # Register project with Watchman (manual mode)")
	fmt.Println("  devbox projects      # List all projects")
	fmt.Println("  devbox watchman mode # Switch watchman mode")
	fmt.Println("  devbox global start  # Start global devbox services")
	fmt.Println("  devbox status        # Show complete status")
	fmt.Println("  devbox network-help  # Network configuration guide")
	fmt.Println()
	fmt.Println("For more information, visit: https://github.com/aliasproject/devbox")
}

func showStatus() error {
	fmt.Printf("Devbox v%s - System Status\n\n", version)

	fmt.Println("📦 Installation Status:")
	fmt.Printf("  Config directory: %s (%s)\n", devboxDir, getStatus(dirExists(devboxDir)))
	fmt.Printf("  Binary location: %s (%s)\n", devboxBinary, getStatus(fileExists(devboxBinary)))
	fmt.Printf("  Env vars file: %s (%s)\n", envVarsFile, getStatus(fileExists(envVarsFile)))
	fmt.Printf("  Docker compose: %s (%s)\n", composeFile, getStatus(fileExists(composeFile)))

	// Environment Variables Status
	fmt.Println("\n🔧 Global Configuration:")
	if isInstalled() {
		loadEnvVars() // Load variables to check them
		fmt.Printf("  Project root: %s\n", getEnvStatus("DEVBOX_PROJECT_ROOT"))
		fmt.Printf("  MySQL version: %s\n", getEnvStatus("MYSQL_SERVER"))
		fmt.Printf("  Watchman mode: %s\n", getEnvStatus("WATCHMAN_MODE"))
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

	// Global Container Status
	if !isInstalled() {
		fmt.Println("  ❌ Devbox not installed - no containers to check")
		return nil
	}

	fmt.Println("\n📊 Global Container Status:")

	// Check global containers
	containers := []string{"devbox_dns", "devbox_nginx", "devbox_watchman", "devbox_mysql", "devbox_mail"}

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
	fmt.Println("  MySQL: localhost:3306")
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
	fmt.Println("🌍 Network Configuration Options for .test Domains\n")

	hostIP, err := getHostIP()
	if err != nil {
		fmt.Println("❌ Could not determine host IP address")
		return
	}

	fmt.Printf("Your host IP: %s\n\n", hostIP)

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
	fmt.Println("  1. Run 'devbox status' to verify containers are running")
	fmt.Printf("  2. Test DNS: nslookup yourproject.test %s\n", hostIP)
	fmt.Println("  3. Test web access from another device")
	fmt.Printf("  4. Quick test: Try accessing http://%s from another device\n", hostIP)
	fmt.Println("     If that works, the issue is just DNS resolution")
	fmt.Println()

	fmt.Println("💡 Troubleshooting:")
	fmt.Println("  • Test direct IP access first to verify web server")
	fmt.Println("  • Ensure devices are on the same network")
	fmt.Println("  • Check firewall settings if connections fail")
}

func getProjectName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "unknown"
	}
	return filepath.Base(cwd)
}

func getProjectConfigPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Join(cwd, ".devbox-project")
}

func getProjectComposePath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Join(cwd, ".devbox", "docker-compose.yml")
}

func isProjectInitialized() bool {
	return fileExists(getProjectConfigPath())
}

func initProject() error {
	if isProjectInitialized() {
		fmt.Println("✅ Project already initialized!")
		return showProjectStatus()
	}

	projectName := getProjectName()
	fmt.Printf("🚀 Initializing devbox project: %s\n\n", projectName)

	// Get PHP version
	fmt.Println("Select PHP version for this project:")
	phpVersion := selectOption("PHP Version", []string{"8.2", "8.1", "8.0", "7.4"})

	// Create project config directory
	projectDir := filepath.Dir(getProjectConfigPath())
	devboxProjectDir := filepath.Join(projectDir, ".devbox")
	if err := os.MkdirAll(devboxProjectDir, 0755); err != nil {
		return fmt.Errorf("failed to create .devbox directory: %w", err)
	}

	// Write project config
	config := fmt.Sprintf("PROJECT_NAME=%s\nPHP_VERSION=%s\n", projectName, phpVersion)
	if err := os.WriteFile(getProjectConfigPath(), []byte(config), 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	// Create project docker-compose.yml
	if err := createProjectCompose(projectName, phpVersion); err != nil {
		return fmt.Errorf("failed to create project compose file: %w", err)
	}

	fmt.Println("✅ Project initialized successfully!")
	fmt.Printf("📁 Project: %s\n", projectName)
	fmt.Printf("🐘 PHP Version: %s\n", phpVersion)
	fmt.Println()

	watchmanMode := os.Getenv("WATCHMAN_MODE")
	fmt.Println("🚀 Next steps:")
	fmt.Println("  devbox start    # Start project containers")
	if watchmanMode == "Manual" {
		fmt.Println("  devbox register # Register project with Watchman for SSL/vhost")
	}
	fmt.Println("  devbox status   # Check project status")

	return nil
}

func createProjectCompose(projectName, phpVersion string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	composeContent := fmt.Sprintf(`services:
  php:
    container_name: devbox_php_%s
    image: ghcr.io/aliasproject/php:%s-fpm
    restart: unless-stopped
    volumes:
      - .:/home/alias
    networks:
      - devbox-network
    external_links:
      - devbox_dns:dns
    dns:
      - 192.168.237.4

networks:
  devbox-network:
    external: true
`, projectName, phpVersion)

	composePath := getProjectComposePath()
	return os.WriteFile(composePath, []byte(composeContent), 0644)
}

func changePHPVersion() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized. Run 'devbox init' first")
	}

	projectName := getProjectName()
	fmt.Printf("🔄 Changing PHP version for project: %s\n\n", projectName)

	// Get current PHP version
	currentVersion := getProjectPHPVersion()
	fmt.Printf("Current PHP version: %s\n\n", currentVersion)

	// Select new PHP version
	newVersion := selectOption("Select new PHP version", []string{"8.2", "8.1", "8.0", "7.4"})

	if newVersion == currentVersion {
		fmt.Println("✅ PHP version unchanged")
		return nil
	}

	// Stop current container
	fmt.Println("🛑 Stopping current PHP container...")
	if err := stopProject(); err != nil {
		fmt.Printf("Warning: Failed to stop current container: %v\n", err)
	}

	// Update project config
	if err := updateProjectConfig("PHP_VERSION", newVersion); err != nil {
		return fmt.Errorf("failed to update project config: %w", err)
	}

	// Recreate docker-compose.yml
	if err := createProjectCompose(projectName, newVersion); err != nil {
		return fmt.Errorf("failed to update compose file: %w", err)
	}

	fmt.Printf("✅ PHP version changed from %s to %s\n", currentVersion, newVersion)
	fmt.Println("🚀 Run 'devbox start' to start with new PHP version")

	return nil
}

func getProjectPHPVersion() string {
	config := getProjectConfig()
	if version, ok := config["PHP_VERSION"]; ok {
		return version
	}
	return "8.2" // default
}

func getProjectConfig() map[string]string {
	config := make(map[string]string)

	content, err := os.ReadFile(getProjectConfigPath())
	if err != nil {
		return config
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				config[parts[0]] = parts[1]
			}
		}
	}

	return config
}

func updateProjectConfig(key, value string) error {
	config := getProjectConfig()
	config[key] = value

	var lines []string
	for k, v := range config {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}

	content := strings.Join(lines, "\n")
	return os.WriteFile(getProjectConfigPath(), []byte(content), 0644)
}

func startProject() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized. Run 'devbox init' first")
	}

	projectName := getProjectName()
	fmt.Printf("🚀 Starting project: %s\n", projectName)

	// Ensure global services are running
	if err := ensureGlobalServices(); err != nil {
		return fmt.Errorf("failed to ensure global services: %w", err)
	}

	// Start project containers
	composePath := getProjectComposePath()
	cmd := exec.Command("docker", "compose", "-f", composePath, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start project containers: %w", err)
	}

	fmt.Println("✅ Project containers started successfully!")
	return showProjectStatus()
}

func stopProject() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized")
	}

	projectName := getProjectName()
	fmt.Printf("🛑 Stopping project: %s\n", projectName)

	composePath := getProjectComposePath()
	cmd := exec.Command("docker", "compose", "-f", composePath, "down")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop project containers: %w", err)
	}

	fmt.Println("✅ Project containers stopped successfully!")
	return nil
}

func startGlobalServices() error {
	fmt.Println("🚀 Starting global devbox services...")

	// Create devbox network if it doesn't exist
	cmd := exec.Command("docker", "network", "create", "devbox-network")
	cmd.Run() // Ignore error - network might already exist

	// Start global services
	cmd = exec.Command("docker", "compose", "-f", composeFile, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start global services: %w", err)
	}

	fmt.Println("✅ Global services started successfully!")
	return nil
}

func stopGlobalServices() error {
	fmt.Println("🛑 Stopping global devbox services...")

	cmd := exec.Command("docker", "compose", "-f", composeFile, "down")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop global services: %w", err)
	}

	fmt.Println("✅ Global services stopped successfully!")
	return nil
}

func ensureGlobalServices() error {
	// Check if global services are running
	cmd := exec.Command("docker", "compose", "-f", composeFile, "ps", "-q")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check global services: %w", err)
	}

	if len(strings.TrimSpace(string(output))) == 0 {
		fmt.Println("🔄 Global services not running, starting them...")
		return startGlobalServices()
	}

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
	fmt.Printf("📁 Directory: %s\n", filepath.Dir(getProjectConfigPath()))

	// Check project container status
	containerName := fmt.Sprintf("devbox_php_%s", projectName)
	status := getContainerStatus(containerName)
	fmt.Printf("🐳 Container: %s (%s)\n", containerName, status)

	// Show project URL and registration status
	fmt.Printf("🌐 Project URL: https://%s.test\n", projectName)

	// Check registration status in manual mode
	watchmanMode := os.Getenv("WATCHMAN_MODE")
	if watchmanMode == "Manual" {
		registrationFile := filepath.Join(devboxDir, "manual-projects", projectName)
		if fileExists(registrationFile) {
			fmt.Printf("📝 Registration: ✅ Registered with Watchman\n")
		} else {
			fmt.Printf("📝 Registration: ❌ Not registered (run 'devbox register')\n")
			fmt.Println("💡 Your project won't have SSL certificates or vhost until registered")
		}
	} else {
		fmt.Printf("📝 Registration: ✅ Auto-watch mode\n")
		fmt.Println("💡 SSL certificates and vhost are created automatically")
	}

	// Show helpful tips based on container status
	if strings.Contains(status, "not") || strings.Contains(status, "❌") {
		fmt.Println("\n🚀 Next steps:")
		fmt.Println("  devbox start    # Start project containers")
	} else if watchmanMode == "Manual" && !fileExists(filepath.Join(devboxDir, "manual-projects", projectName)) {
		fmt.Println("\n🚀 Next steps:")
		fmt.Println("  devbox register # Register with Watchman for SSL/vhost")
	}

	return nil
}

func listProjects() error {
	fmt.Println("📁 Devbox Projects\n")

	projectRoot := os.Getenv("DEVBOX_PROJECT_ROOT")
	if projectRoot == "" {
		return fmt.Errorf("project root not configured")
	}

	// Find all directories with .devbox-project files
	var projects []ProjectInfo
	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if info.Name() == ".devbox-project" {
			projectDir := filepath.Dir(path)
			projectName := filepath.Base(projectDir)

			// Read project config
			config := getProjectConfigFromPath(path)
			phpVersion := config["PHP_VERSION"]
			if phpVersion == "" {
				phpVersion = "8.2"
			}

			// Check container status
			containerName := fmt.Sprintf("devbox_php_%s", projectName)
			status := getContainerStatus(containerName)

			projects = append(projects, ProjectInfo{
				Name:       projectName,
				Path:       projectDir,
				PHPVersion: phpVersion,
				Status:     status,
			})
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to scan projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("No devbox projects found.")
		fmt.Printf("Projects are searched in: %s\n", projectRoot)
		fmt.Println("\n💡 To create a project:")
		fmt.Println("  1. Navigate to your project directory")
		fmt.Println("  2. Run 'devbox init'")
		return nil
	}

	// Display projects
	fmt.Printf("Found %d project(s):\n\n", len(projects))
	for _, project := range projects {
		fmt.Printf("📦 %s\n", project.Name)
		fmt.Printf("   Path: %s\n", project.Path)
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

func getProjectConfigFromPath(configPath string) map[string]string {
	config := make(map[string]string)

	content, err := os.ReadFile(configPath)
	if err != nil {
		return config
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.Contains(line, "=") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				config[parts[0]] = parts[1]
			}
		}
	}

	return config
}

func registerProject() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized. Run 'devbox init' first")
	}

	projectName := getProjectName()
	fmt.Printf("📝 Registering project with Watchman: %s\n", projectName)

	// Check if Watchman is in manual mode
	watchmanMode := os.Getenv("WATCHMAN_MODE")
	if watchmanMode != "Manual" {
		fmt.Printf("⚠️  Watchman is in %s mode\n", watchmanMode)
		fmt.Println("💡 Registration is automatic in Auto-watch mode")
		return nil
	}

	// Get current working directory
	projectPath, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Create a registration file that Watchman can detect
	registrationFile := filepath.Join(devboxDir, "manual-projects", projectName)
	registrationDir := filepath.Dir(registrationFile)

	if err := os.MkdirAll(registrationDir, 0755); err != nil {
		return fmt.Errorf("failed to create registration directory: %w", err)
	}

	registrationData := fmt.Sprintf("PROJECT_NAME=%s\nPROJECT_PATH=%s\nREGISTERED_AT=%s\n",
		projectName, projectPath, time.Now().Format(time.RFC3339))

	if err := os.WriteFile(registrationFile, []byte(registrationData), 0644); err != nil {
		return fmt.Errorf("failed to create registration file: %w", err)
	}

	// Signal Watchman to process the registration
	cmd := exec.Command("docker", "exec", "devbox_watchman", "touch", "/tmp/manual-project-added")
	if err := cmd.Run(); err != nil {
		fmt.Printf("⚠️  Warning: Failed to signal Watchman: %v\n", err)
		fmt.Println("💡 Watchman will detect the registration on its next scan")
	}

	fmt.Println("✅ Project registered successfully!")
	fmt.Printf("🌐 Your project will be available at: https://%s.test\n", projectName)
	fmt.Println("⏳ SSL certificate and vhost will be created shortly")

	return nil
}

func unregisterProject() error {
	if !isProjectInitialized() {
		return fmt.Errorf("project not initialized")
	}

	projectName := getProjectName()
	fmt.Printf("🗑️  Unregistering project from Watchman: %s\n", projectName)

	// Remove registration file
	registrationFile := filepath.Join(devboxDir, "manual-projects", projectName)
	if err := os.Remove(registrationFile); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove registration file: %w", err)
		}
		fmt.Println("⚠️  Project was not registered")
		return nil
	}

	// Signal Watchman to process the unregistration
	cmd := exec.Command("docker", "exec", "devbox_watchman", "touch", "/tmp/manual-project-removed")
	if err := cmd.Run(); err != nil {
		fmt.Printf("⚠️  Warning: Failed to signal Watchman: %v\n", err)
		fmt.Println("💡 Watchman will detect the unregistration on its next scan")
	}

	fmt.Println("✅ Project unregistered successfully!")
	fmt.Printf("🌐 https://%s.test will no longer be available\n", projectName)
	fmt.Println("💡 SSL certificates and vhost files may still exist until cleanup")

	return nil
}

func switchWatchmanMode() error {
	fmt.Println("🔄 Switching Watchman Mode\n")

	currentMode := os.Getenv("WATCHMAN_MODE")
	if currentMode == "" {
		currentMode = "Auto-watch"
	}

	fmt.Printf("Current mode: %s\n\n", currentMode)
	fmt.Println("Available modes:")
	fmt.Println("  Auto-watch - Automatically detect and configure new projects in project root directory")
	fmt.Println("  Manual - Manually register projects using 'devbox register' command")
	fmt.Println()

	newMode := selectOption("Select new Watchman mode", []string{"Auto-watch", "Manual"})

	if newMode == currentMode {
		fmt.Println("✅ Watchman mode unchanged")
		return nil
	}

	// Update environment variable
	if err := updateEnvVar("WATCHMAN_MODE", newMode); err != nil {
		return fmt.Errorf("failed to update watchman mode: %w", err)
	}

	// Set in current process
	os.Setenv("WATCHMAN_MODE", newMode)

	// Restart Watchman container to pick up new mode
	fmt.Println("🔄 Restarting Watchman container...")
	cmd := exec.Command("docker", "compose", "-f", composeFile, "restart", "watchman")
	if err := cmd.Run(); err != nil {
		fmt.Printf("⚠️  Warning: Failed to restart Watchman: %v\n", err)
		fmt.Println("💡 Please restart manually: devbox global stop && devbox global start")
	}

	fmt.Printf("✅ Watchman mode changed to: %s\n", newMode)

	if newMode == "Manual" {
		fmt.Println("\n💡 Manual mode tips:")
		fmt.Println("  • Use 'devbox register' in project directories to register them")
		fmt.Println("  • Use 'devbox unregister' to remove projects")
		fmt.Println("  • Use 'devbox watchman status' to see registered projects")
	} else {
		fmt.Println("\n💡 Auto-watch mode tips:")
		fmt.Println("  • Projects in your root directory will be detected automatically")
		fmt.Println("  • No manual registration needed")
		fmt.Println("  • New folders will be picked up automatically")
	}

	return nil
}

func showWatchmanStatus() error {
	fmt.Println("👁️  Watchman Status\n")

	// Check if Watchman container is running
	status := getContainerStatus("devbox_watchman")
	fmt.Printf("Container Status: %s\n", status)

	watchmanMode := os.Getenv("WATCHMAN_MODE")
	if watchmanMode == "" {
		watchmanMode = "Auto-watch"
	}
	fmt.Printf("Current Mode: %s\n", watchmanMode)

	if watchmanMode == "Manual" {
		fmt.Println("\n📝 Manually Registered Projects:")
		registrationDir := filepath.Join(devboxDir, "manual-projects")

		if !dirExists(registrationDir) {
			fmt.Println("  No projects registered")
			return nil
		}

		files, err := os.ReadDir(registrationDir)
		if err != nil {
			return fmt.Errorf("failed to read registration directory: %w", err)
		}

		if len(files) == 0 {
			fmt.Println("  No projects registered")
		} else {
			for _, file := range files {
				if !file.IsDir() {
					projectName := file.Name()
					registrationFile := filepath.Join(registrationDir, projectName)
					config := getProjectConfigFromPath(registrationFile)
					projectPath := config["PROJECT_PATH"]
					registeredAt := config["REGISTERED_AT"]

					fmt.Printf("  📦 %s\n", projectName)
					if projectPath != "" {
						fmt.Printf("     Path: %s\n", projectPath)
					}
					if registeredAt != "" {
						fmt.Printf("     Registered: %s\n", registeredAt)
					}
					fmt.Printf("     URL: https://%s.test\n", projectName)
					fmt.Println()
				}
			}
		}
	} else {
		fmt.Println("\n🔍 Auto-watch Configuration:")
		projectRoot := os.Getenv("DEVBOX_PROJECT_ROOT")
		fmt.Printf("  Watching directory: %s\n", projectRoot)
		fmt.Println("  Projects are detected automatically when folders are added")
		fmt.Println("  💡 Use 'devbox projects' to see all detected projects")
	}

	return nil
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
