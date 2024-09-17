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
)

var (
	version      = "0.3.0"
	composeFile  = filepath.Join(os.Getenv("HOME"), ".devbox", "docker-compose.yml")
	githubRepo   = "aliasproject/devbox"
	devboxBinary = "/usr/local/bin/devbox"
	env_vars     = filepath.Join(os.Getenv("HOME"), ".devbox/env_vars")
	certsPath    = filepath.Join(os.Getenv("HOME"), ".devbox", "AliasProjectCASelfSigned.pem")
)

func checkForUpdates() {
	fmt.Println("Checking for updates...")
	latestVersion, err := getLatestVersion()
	if err != nil {
		fmt.Println("Error: Failed to fetch the latest version. Please check your internet connection and try again.")
		return
	}

	if latestVersion != version {
		fmt.Printf("A new version is available: %s\n", latestVersion)
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Do you want to update? (y/n) ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if strings.ToLower(input) == "y" {
			updateDevbox(latestVersion)
		}
	} else {
		fmt.Println("You are using the latest version.")
	}
}

func getLatestVersion() (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", fmt.Sprintf("token %s", os.Getenv("GITHUB_TOKEN")))

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch latest version, status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if tag, ok := result["tag_name"].(string); ok {
		return tag, nil
	}
	return "", fmt.Errorf("unable to parse version information")
}

func updateDevbox(latestVersion string) {
	fmt.Println("Updating devbox...")
	tmpFile := "/tmp/devbox"

	cmd := exec.Command("curl", "-L", "-H", fmt.Sprintf("Authorization: token %s", os.Getenv("GITHUB_TOKEN")),
		fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo))
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("Error: Failed to download the update.")
		return
	}

	err = cmd.Start()
	if err != nil {
		fmt.Println("Error: Failed to download the update.")
		return
	}

	downloadURL := extractDownloadURL(pipe)
	cmd.Wait()

	if downloadURL != "" {
		err = downloadAndInstall(tmpFile, downloadURL)
		if err != nil {
			fmt.Println("Error during update:", err)
		} else {
			fmt.Println("Update completed. Please restart devbox.")
		}
	}
}

func extractDownloadURL(pipe io.Reader) string {
	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "browser_download_url") && strings.Contains(line, "devbox") {
			url := strings.Split(line, ":")[1]
			return strings.Trim(url, "\" ")
		}
	}
	return ""
}

func downloadAndInstall(tmpFile, url string) error {
	fmt.Println("Downloading update...")
	cmd := exec.Command("wget", "-q", "-O", tmpFile, url)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download the update: %v", err)
	}

	fmt.Println("Installing update...")
	err := exec.Command("sudo", "mv", tmpFile, devboxBinary).Run()
	if err != nil {
		return fmt.Errorf("failed to move the updated file: %v", err)
	}

	err = exec.Command("sudo", "chmod", "+x", devboxBinary).Run()
	if err != nil {
		return fmt.Errorf("failed to set executable permissions: %v", err)
	}

	return nil
}

func main() {
	fmt.Println("Start.")
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".devbox")); os.IsNotExist(err) || !fileExists(devboxBinary) {
		fmt.Println("devbox is not installed.")
		// Optionally, call installation logic here
		install()
		return
	}

	// Check for updates
	checkForUpdates()

	// Additional functionality for docker compose commands
	// Example: dockerComposeCommand([]string{"ps"})
}

func install() {
	fmt.Println("Installing devbox...")

	// Check if Docker is installed
	if !isDockerInstalled() {
		fmt.Println("Error: Docker is not installed. Please install Docker and try again.")
		return
	}

	// Create .devbox directory and copy YML
	fmt.Println("CREATE DIRECTORY!")
	if err := os.Mkdir(filepath.Join(os.Getenv("HOME"), ".devbox"), 0755); err != nil {
		fmt.Println("Error: Unable to create .devbox directory:", err)
		return
	}

	if err := copyFile("docker-compose.yml", filepath.Join(os.Getenv("HOME"), ".devbox/docker-compose.yml")); err != nil {
		fmt.Println("Error: Unable to copy docker-compose.yml:", err)
		return
	}

	// Copy CLI binary
	// if err := exec.Command("sudo", "cp", "devbox", "/usr/local/bin/devbox").Run(); err != nil {
	// 	fmt.Println("Error: Unable to copy devbox binary:", err)
	// 	return
	// }

	// Projects Directory input
	devboxProjectRoot := ""
	for devboxProjectRoot == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Please enter the path to your projects directory: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Expand ~/ to the user's home directory
		if strings.HasPrefix(input, "~/") {
			input = filepath.Join(os.Getenv("HOME"), input[2:])
		}

		if _, err := os.Stat(input); !os.IsNotExist(err) {
			devboxProjectRoot = filepath.Clean(input) + string(os.PathSeparator)
			writeToFile(env_vars, fmt.Sprintf("export DEVBOX_PROJECT_ROOT=%s", devboxProjectRoot))
		} else {
			fmt.Println("Invalid directory, please try again.")
		}
	}

	// PHP Version selection
	phpVersion := selectOption("Select PHP Version", []string{"8.2", "8.1", "8.0"})
	writeToFile(env_vars, fmt.Sprintf("export PHP_VERSION=%s", phpVersion))

	// MySQL Version selection
	mysqlVersion := selectOption("Select MySQL Version", []string{"8.0", "5.7"})
	writeToFile(env_vars, fmt.Sprintf("export MYSQL_SERVER=%s", mysqlVersion))

	// Reload ENV Vars
	fmt.Println("Reloading environment variables...")
	cmd := exec.Command("sh", "-c", fmt.Sprintf("source %s", env_vars))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Error reloading environment variables: %v\n", err)
	}

	// Configure DNS
	fmt.Println("Configuring DNS...")
	if _, err := os.Stat("/etc/resolver"); os.IsNotExist(err) {
		exec.Command("sudo", "mkdir", "/etc/resolver").Run()
	}

	if _, err := os.Stat("/etc/resolver/test"); os.IsNotExist(err) {
		dnsConfig := "port 5354\nnameserver 127.0.0.1"
		exec.Command("sudo", "tee", "/etc/resolver/test").Run()
		writeToFile("/etc/resolver/test", dnsConfig)
	}

	// Start environment
	fmt.Println("Starting Containers...")
	startDockerCompose()

	// Wait for Watchman container to become healthy
	fmt.Println("Preparing sites...")
	for !isWatchmanHealthy() {
	}

	// Trust SSL Root Authority
	fmt.Println("Trusting Certificate")
	if err := exec.Command("docker", "compose", "cp", "watchman:/certs/AliasProjectCASelfSigned.pem", certsPath).Run(); err != nil {
		fmt.Println("Error: Unable to copy certificate:", err)
		return
	}

	if err := exec.Command("sudo", "security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", certsPath).Run(); err != nil {
		fmt.Println("Error: Unable to trust certificate:", err)
		return
	}

	// Installing Composer
	fmt.Println("Installing Composer...")
	if err := exec.Command("docker", "compose", "exec", "php", "sh", "-c", "wget https://getcomposer.org/composer-stable.phar -O /usr/local/bin/composer && chmod +x /usr/local/bin/composer").Run(); err != nil {
		fmt.Println("Error: Unable to install Composer:", err)
		return
	}

	// Installing NPM
	fmt.Println("Installing NPM...")
	if err := exec.Command("docker", "compose", "exec", "php", "sh", "-c", "apk add --no-cache npm").Run(); err != nil {
		fmt.Println("Error: Unable to install NPM:", err)
		return
	}

	fmt.Println("devbox installed!")
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
	if err != nil {
		return err
	}

	return out.Close()
}

func writeToFile(filepath, content string) {
	f, err := os.OpenFile(filepath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		fmt.Println("Error writing to file:", err)
		return
	}
	defer f.Close()
	f.WriteString(content + "\n")
}

func selectOption(prompt string, options []string) string {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println(prompt)
		for i, option := range options {
			fmt.Printf("[%d] %s\n", i+1, option)
		}
		fmt.Print("Enter the number: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		index, err := strconv.Atoi(input)

		if err == nil && index >= 1 && index <= len(options) {
			return options[index-1]
		} else {
			fmt.Println("Invalid option, please try again.")
		}
	}
}

func startDockerCompose() {
	cmd := exec.Command("docker", "compose", "-f", composeFile, "up", "-d")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		fmt.Println("Error starting Docker Compose:", err)
	}
}

func isWatchmanHealthy() bool {
	cmd := exec.Command("docker", "inspect", "-f", "{{.State.Health.Status}}", "devbox_watchman")
	output, err := cmd.Output()
	if err != nil {
		fmt.Println("Error checking Watchman container status:", err)
		return false
	}
	return strings.TrimSpace(string(output)) == "healthy"
}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	return err == nil && !info.IsDir()
}

func dockerComposeCommand(args []string) {
	compose := fmt.Sprintf("docker compose -f %s", composeFile)
	cmd := exec.Command("/bin/sh", "-c", compose+" "+strings.Join(args, " "))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running Docker Compose: %v\n", err)
	}
}
