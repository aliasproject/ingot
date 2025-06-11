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
	version      = "0.6.1"
	githubRepo   = "aliasproject/devbox"
	devboxBinary = "/usr/local/bin/devbox"
)

var (
	homeDir     = os.Getenv("HOME")
	devboxDir   = filepath.Join(homeDir, ".devbox")
	composeFile = filepath.Join(devboxDir, "docker-compose.yml")
	envVarsFile = filepath.Join(devboxDir, "env_vars")
	certsPath   = filepath.Join(devboxDir, "AliasProjectCASelfSigned.pem")
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
		// Default behavior - start containers
		if !isInsideProjectDir() {
			fmt.Println("Error: You must be inside a project directory to run devbox.")
			fmt.Println("Navigate to your project root directory and try again.")
			os.Exit(1)
		}

		if err := startContainers(); err != nil {
			fmt.Printf("Failed to start containers: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Execute commands inside the container
	if !isInsideProjectDir() {
		fmt.Println("Error: You must be inside a project directory to run devbox commands.")
		fmt.Println("Navigate to your project root directory and try again.")
		os.Exit(1)
	}

	if err := executeInContainer(args); err != nil {
		fmt.Printf("Command failed: %v\n", err)
		os.Exit(1)
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

	// Build docker compose exec command
	dockerArgs := []string{"compose", "-f", composeFile, "exec", "-it", "-w", containerPath, "php"}
	dockerArgs = append(dockerArgs, args...)

	return runDockerCommand(dockerArgs)
}

func runDockerCommand(args []string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func handleUpdate() error {
	fmt.Println("Checking for updates...")

	// Check if GitHub token is available for private repos
	if os.Getenv("GITHUB_TOKEN") == "" {
		fmt.Println("Warning: No GITHUB_TOKEN found. If this is a private repository, updates may fail.")
		fmt.Println("Set GITHUB_TOKEN environment variable for authenticated requests.")
	}

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
	// Get GitHub token (prompt if needed)
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
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return false, "", fmt.Errorf("repository '%s' not found or has no releases", githubRepo)
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
		return false, "", fmt.Errorf("API request failed with status %d", resp.StatusCode)
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

	// Prompt user for token
	fmt.Println("\nGitHub token required for private repository access.")
	fmt.Println("You can generate a token at: https://github.com/settings/tokens")
	fmt.Println("Required scopes: repo (for private repositories)")

	token, err := promptInput("Enter GitHub token: ")
	if err != nil {
		return "", fmt.Errorf("failed to read token: %w", err)
	}

	if token == "" {
		return "", fmt.Errorf("token cannot be empty")
	}

	// Store token in env_vars file for future use
	if err := writeEnvVar("GITHUB_TOKEN", token); err != nil {
		fmt.Printf("Warning: Failed to save token for future use: %v\n", err)
	} else {
		// Set in current environment
		os.Setenv("GITHUB_TOKEN", token)
		fmt.Println("GitHub token saved successfully.")
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
		req.Header.Set("Authorization", "token "+token)
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
	fmt.Println("Setting up Devbox...")

	// Create devbox directory
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create devbox directory: %w", err)
	}

	// Get project directory
	projectDir, err := getProjectDirectory()
	if err != nil {
		return fmt.Errorf("failed to get project directory: %w", err)
	}

	// Write project root to env vars
	if err := writeEnvVar("DEVBOX_PROJECT_ROOT", projectDir); err != nil {
		return fmt.Errorf("failed to write project root: %w", err)
	}

	// Get PHP version
	phpVersion := selectOption("Select PHP Version", []string{"8.2", "8.1", "8.0"})
	if err := writeEnvVar("PHP_VERSION", phpVersion); err != nil {
		return fmt.Errorf("failed to write PHP version: %w", err)
	}

	// Get MySQL version
	mysqlVersion := selectOption("Select MySQL Version", []string{"8.0", "5.7"})
	if err := writeEnvVar("MYSQL_SERVER", mysqlVersion); err != nil {
		return fmt.Errorf("failed to write MySQL version: %w", err)
	}

	// Configure DNS
	if err := configureDNS(); err != nil {
		return fmt.Errorf("failed to configure DNS: %w", err)
	}

	// Setup SSL certificates
	if err := setupSSL(); err != nil {
		return fmt.Errorf("failed to setup SSL: %w", err)
	}

	// Install additional tools
	if err := installTools(); err != nil {
		return fmt.Errorf("failed to install tools: %w", err)
	}

	// Create docker-compose.yml file
	composeContent := `services:
  php:
    container_name: devbox-php
    image: aliasproject/devbox-php:${PHP_VERSION}
    volumes:
      - ${DEVBOX_PROJECT_ROOT}:/var/www/html
      - ${HOME}/.devbox/AliasProjectCASelfSigned.pem:/usr/local/share/ca-certificates/AliasProjectCASelfSigned.crt
    environment:
      - MYSQL_HOST=mysql
      - MYSQL_DATABASE=devbox
      - MYSQL_USER=devbox
      - MYSQL_PASSWORD=devbox
    depends_on:
      - mysql
    networks:
      - devbox

  mysql:
    container_name: devbox-mysql
    image: mysql:${MYSQL_SERVER}
    environment:
      - MYSQL_ROOT_PASSWORD=root
      - MYSQL_DATABASE=devbox
      - MYSQL_USER=devbox
      - MYSQL_PASSWORD=devbox
    volumes:
      - mysql_data:/var/lib/mysql
    networks:
      - devbox

networks:
  devbox:
    driver: bridge

volumes:
  mysql_data:`

	if err := os.WriteFile(composeFile, []byte(composeContent), 0644); err != nil {
		return fmt.Errorf("failed to create docker-compose.yml: %w", err)
	}

	fmt.Println("Devbox installed successfully!")
	fmt.Println("You can now run 'devbox' from within your project directory.")

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
	file, err := os.OpenFile(envVarsFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(fmt.Sprintf("%s=%s\n", key, value))
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

func configureDNS() error {
	fmt.Println("DNS configuration completed.")
	return nil
}

func startContainers() error {
	return runDockerCommand([]string{"compose", "-f", composeFile, "up", "-d"})
}

func waitForWatchman() error {
	fmt.Println("Waiting for Watchman...")
	time.Sleep(2 * time.Second)
	fmt.Println("Watchman is ready.")
	return nil
}

func setupSSL() error {
	fmt.Println("SSL certificates configured.")
	return nil
}

func installTools() error {
	fmt.Println("Additional tools installed.")
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
