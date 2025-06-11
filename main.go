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
	version      = "0.4.5"
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
		ID                 int64  `json:"id"`
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func main() {
	// Check if devbox is installed
	if !isInstalled() {
		fmt.Println("devbox is not installed.")
		if err := install(); err != nil {
			fmt.Printf("Installation failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Load environment variables
	if err := loadEnvVars(); err != nil {
		fmt.Printf("Error loading environment variables: %v\n", err)
		os.Exit(1)
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

	// Check if inside project directory (except for certain commands)
	if len(args) == 0 || !isInsideProjectDir() {
		if len(args) > 0 {
			fmt.Printf("Please run from inside projects directory: %s\n", os.Getenv("DEVBOX_PROJECT_ROOT"))
			os.Exit(1)
		}
		// Show status if no args
		runDockerCommand([]string{"ps"})
		return
	}

	// Get current directory relative to project root
	relativeDir, err := getRelativeProjectPath()
	if err != nil {
		fmt.Printf("Error getting project path: %v\n", err)
		os.Exit(1)
	}

	// Check if directory exists in container
	dirExists, err := directoryExistsInContainer(relativeDir)
	if err != nil {
		fmt.Printf("Error checking directory: %v\n", err)
		os.Exit(1)
	}

	// Execute commands
	if dirExists {
		executeInContainer(relativeDir, args)
	} else {
		runDockerCommand(args)
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
	pwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	projectRoot := os.Getenv("DEVBOX_PROJECT_ROOT")
	relPath := strings.TrimPrefix(pwd, projectRoot)
	relPath = strings.TrimPrefix(relPath, "/")

	return relPath, nil
}

func directoryExistsInContainer(dir string) (bool, error) {
	if dir == "" {
		return true, nil
	}

	cmd := exec.Command("docker", "compose", "-f", composeFile, "exec", "-T", "php",
		"sh", "-c", fmt.Sprintf("test -d /home/alias/%s && echo true || echo false", dir))

	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check directory: %w", err)
	}

	return strings.TrimSpace(string(output)) == "true", nil
}

func executeInContainer(dir string, args []string) {
	if len(args) == 0 {
		return
	}

	var command string
	switch args[0] {
	case "artisan":
		command = "php artisan " + strings.Join(args[1:], " ")
	case "composer":
		command = "composer " + strings.Join(args[1:], " ")
	case "npm":
		command = "npm " + strings.Join(args[1:], " ")
	default:
		command = strings.Join(args, " ")
	}

	cmd := exec.Command("docker", "compose", "-f", composeFile, "exec",
		"-w", fmt.Sprintf("/home/alias/%s", dir), "php", "sh", "-c", command)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

func runDockerCommand(args []string) {
	fullArgs := append([]string{"compose", "-f", composeFile}, args...)
	cmd := exec.Command("docker", fullArgs...)

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
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
		fmt.Printf("Update check failed: %v\n", err)
		if strings.Contains(err.Error(), "401") {
			fmt.Println("This appears to be a private repository. Please set GITHUB_TOKEN environment variable.")
		}
		return nil
	}

	if !hasUpdate {
		fmt.Printf("You are using the latest version (%s).\n", version)
		return nil
	}

	fmt.Printf("New version available: %s (current: %s)\n", latestVersion, version)
	if !promptYesNo("Do you want to update?") {
		return nil
	}

	return performUpdate(latestVersion)
}

func checkForUpdates() (bool, string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo), nil)
	if err != nil {
		return false, "", err
	}

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
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
		return false, "", fmt.Errorf("unauthorized access - set GITHUB_TOKEN for private repositories")
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

	return rel.TagName != version, rel.TagName, nil
}

func performUpdate(newVersion string) error {
	// Check if GitHub token is available for private repo downloads
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		fmt.Printf("\nAutomatic update requires GITHUB_TOKEN for private repository access.\n")
		fmt.Printf("\nTo update manually:\n")
		fmt.Printf("1. Visit: https://github.com/%s/releases/tag/%s\n", githubRepo, newVersion)
		fmt.Printf("2. Download the source code or binary for your system\n")
		fmt.Printf("3. Build and install, or replace the binary at: %s\n", devboxBinary)
		return nil
	}

	downloadURL, err := getDownloadURL()
	if err != nil {
		fmt.Printf("\nAutomatic update not available: %v\n", err)
		fmt.Printf("\nTo update manually:\n")
		fmt.Printf("1. Visit: https://github.com/%s/releases/tag/%s\n", githubRepo, newVersion)
		fmt.Printf("2. Download the source code or binary for your system\n")
		fmt.Printf("3. Build and install, or replace the binary at: %s\n", devboxBinary)
		return nil
	}

	tmpFile := "/tmp/devbox"
	if err := downloadFile(downloadURL, tmpFile); err != nil {
		if strings.Contains(err.Error(), "404") {
			fmt.Printf("\nDownload failed: Asset not found or access denied.\n")
			fmt.Printf("This may require using the GitHub API asset download endpoint.\n")
			fmt.Printf("\nTo update manually:\n")
			fmt.Printf("1. Visit: https://github.com/%s/releases/tag/%s\n", githubRepo, newVersion)
			fmt.Printf("2. Download the appropriate asset\n")
			fmt.Printf("3. Replace the binary at: %s\n", devboxBinary)
			return nil
		}
		return fmt.Errorf("failed to download: %w", err)
	}

	return installBinary(tmpFile)
}

func getDownloadURL() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo), nil)
	if err != nil {
		return "", err
	}

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

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

func downloadFile(url, dest string) error {
	fmt.Println("Downloading update...")

	// Use Go's HTTP client with authentication for private repos
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	// Add GitHub token for private repo access
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN required for private repository downloads")
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Errorf("asset not found (404) - may need to use API asset download endpoint")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Create the destination file
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer out.Close()

	// Copy the response body to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func installBinary(tmpFile string) error {
	fmt.Println("Installing update...")

	if err := exec.Command("sudo", "mv", tmpFile, devboxBinary).Run(); err != nil {
		return fmt.Errorf("failed to move binary: %w", err)
	}

	if err := exec.Command("sudo", "chmod", "+x", devboxBinary).Run(); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	fmt.Println("Update completed successfully!")
	return nil
}

func install() error {
	fmt.Println("Installing devbox...")

	if !isDockerInstalled() {
		return fmt.Errorf("Docker is not installed")
	}

	// Create devbox directory
	if err := os.MkdirAll(devboxDir, 0755); err != nil {
		return fmt.Errorf("failed to create devbox directory: %w", err)
	}

	// Copy docker-compose.yml
	if err := copyFile("docker-compose.yml", composeFile); err != nil {
		return fmt.Errorf("failed to copy docker-compose.yml: %w", err)
	}

	// Get project directory
	projectRoot := getProjectDirectory()
	if err := writeEnvVar("DEVBOX_PROJECT_ROOT", projectRoot); err != nil {
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
		fmt.Printf("Warning: DNS configuration failed: %v\n", err)
	}

	// Start containers
	fmt.Println("Starting containers...")
	if err := startContainers(); err != nil {
		return fmt.Errorf("failed to start containers: %w", err)
	}

	// Wait for containers to be ready
	fmt.Println("Waiting for containers to be ready...")
	if err := waitForWatchman(); err != nil {
		return fmt.Errorf("watchman container failed to start: %w", err)
	}

	// Setup SSL certificate
	if err := setupSSL(); err != nil {
		fmt.Printf("Warning: SSL setup failed: %v\n", err)
	}

	// Install tools
	if err := installTools(); err != nil {
		fmt.Printf("Warning: Tool installation failed: %v\n", err)
	}

	fmt.Println("devbox installed successfully!")
	return nil
}

func isDockerInstalled() bool {
	_, err := exec.LookPath("docker")
	return err == nil
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

func getProjectDirectory() string {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("Enter path to your projects directory: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if strings.HasPrefix(input, "~/") {
			input = filepath.Join(homeDir, input[2:])
		}

		if _, err := os.Stat(input); err == nil {
			return filepath.Clean(input)
		}

		fmt.Println("Invalid directory, please try again.")
	}
}

func selectOption(prompt string, options []string) string {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println(prompt)
		for i, option := range options {
			fmt.Printf("[%d] %s\n", i+1, option)
		}
		fmt.Print("Enter number: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if index, err := strconv.Atoi(input); err == nil && index >= 1 && index <= len(options) {
			return options[index-1]
		}

		fmt.Println("Invalid option, please try again.")
	}
}

func configureDNS() error {
	if err := exec.Command("sudo", "mkdir", "-p", "/etc/resolver").Run(); err != nil {
		return err
	}

	dnsConfig := "port 5354\nnameserver 127.0.0.1\n"
	cmd := exec.Command("sudo", "tee", "/etc/resolver/test")
	cmd.Stdin = strings.NewReader(dnsConfig)

	return cmd.Run()
}

func startContainers() error {
	cmd := exec.Command("docker", "compose", "-f", composeFile, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForWatchman() error {
	for i := 0; i < 60; i++ { // Wait up to 60 seconds
		cmd := exec.Command("docker", "inspect", "-f", "{{.State.Health.Status}}", "devbox_watchman")
		if output, err := cmd.Output(); err == nil {
			if strings.TrimSpace(string(output)) == "healthy" {
				return nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("watchman container did not become healthy")
}

func setupSSL() error {
	if err := exec.Command("docker", "compose", "-f", composeFile, "cp",
		"watchman:/certs/AliasProjectCASelfSigned.pem", certsPath).Run(); err != nil {
		return err
	}

	return exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot",
		"-k", "/Library/Keychains/System.keychain", certsPath).Run()
}

func installTools() error {
	// Install Composer
	composerCmd := "wget -q https://getcomposer.org/composer-stable.phar -O /usr/local/bin/composer && chmod +x /usr/local/bin/composer"
	if err := exec.Command("docker", "compose", "-f", composeFile, "exec", "php", "sh", "-c", composerCmd).Run(); err != nil {
		return fmt.Errorf("failed to install composer: %w", err)
	}

	// Install NPM
	if err := exec.Command("docker", "compose", "-f", composeFile, "exec", "php", "sh", "-c", "apk add --no-cache npm").Run(); err != nil {
		return fmt.Errorf("failed to install npm: %w", err)
	}

	return nil
}

func promptYesNo(question string) bool {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s (y/n): ", question)

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	return input == "y" || input == "yes"
}
