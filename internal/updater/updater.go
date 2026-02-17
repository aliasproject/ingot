package updater

import (
	"github.com/aliasproject/devbox/internal/utils"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aliasproject/devbox/internal/config"
)

// Release contains minimal update information
type Release struct {
	Version   string            `json:"version"`
	Date      string            `json:"date"`
	Notes     string            `json:"notes"`
	Critical  bool              `json:"critical"`
	Downloads map[string]Binary `json:"downloads"`
}

// Binary contains download information for a specific platform
type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		ID                 int64  `json:"id"`
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// Updater handles self-updates
type Updater struct {
	manifestURL string
	httpClient  *http.Client
	githubToken string
	githubRepo  string
}

// New creates a new updater instance
func New() *Updater {
	// Get manifest URL from environment or use default
	manifestURL := os.Getenv("DEVBOX_UPDATE_URL")
	if manifestURL == "" {
		manifestURL = "https://devbox-updates.s3.amazonaws.com/stable/manifest.json"
	}

	// Get GitHub configuration
	githubRepo := os.Getenv("DEVBOX_GITHUB_REPO")
	if githubRepo == "" {
		githubRepo = "aliasproject/devbox" // default repo
	}

	return &Updater{
		manifestURL: manifestURL,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		githubRepo:  githubRepo,
	}
}

// CheckForUpdate checks if a new version is available
func (u *Updater) CheckForUpdate() (*Release, bool, error) {
	// Try GitHub releases first if token is available
	if u.githubToken != "" {
		release, err := u.fetchLatestGitHubRelease()
		if err == nil {
			isNewer := compareVersions(release.Version, config.Version) > 0
			return release, isNewer, nil
		}
		// If GitHub fails, fall back to manifest
		fmt.Printf("Warning: GitHub release check failed: %v\nFalling back to manifest...\n", err)
	}

	// Fall back to S3 manifest
	release, err := u.fetchLatestRelease()
	if err != nil {
		return nil, false, err
	}

	isNewer := compareVersions(release.Version, config.Version) > 0
	return release, isNewer, nil
}

// Update performs the self-update
func (u *Updater) Update(force bool) error {
	// Check for updates
	release, isNewer, err := u.CheckForUpdate()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !isNewer && !force {
		fmt.Printf("Already running the latest version (v%s)\n", config.Version)
		return nil
	}

	// Get binary for current platform
	platform := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
	binary, ok := release.Downloads[platform]
	if !ok {
		return fmt.Errorf("no binary available for %s", platform)
	}

	// Show update info
	if release.Critical {
		fmt.Println(utils.Colorize("[WARNING]", utils.ColorYellow) + " This is a critical security update")
	}
	if release.Notes != "" {
		fmt.Printf("\nRelease notes:\n%s\n", release.Notes)
	}

	// Download and replace
	fmt.Printf("\nDownloading update (%.1f MB)...\n", float64(binary.Size)/(1024*1024))
	return u.performUpdate(&binary)
}

// SetGitHubToken sets the GitHub token for accessing private repositories
func (u *Updater) SetGitHubToken(token string) {
	u.githubToken = token
}

// fetchLatestRelease fetches the latest release information from S3 manifest
func (u *Updater) fetchLatestRelease() (*Release, error) {
	resp, err := u.httpClient.Get(u.manifestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch manifest: %s", resp.Status)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// fetchLatestGitHubRelease fetches the latest release from GitHub
func (u *Updater) fetchLatestGitHubRelease() (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", u.githubRepo)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if u.githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+u.githubToken)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch GitHub release: %s", resp.Status)
	}

	var githubRelease GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&githubRelease); err != nil {
		return nil, err
	}

	// Skip drafts and prereleases
	if githubRelease.Draft || githubRelease.Prerelease {
		return nil, fmt.Errorf("latest release is a draft or prerelease")
	}

	// Convert GitHub release to our Release format
	release := &Release{
		Version:   strings.TrimPrefix(githubRelease.TagName, "v"),
		Date:      githubRelease.PublishedAt,
		Notes:     githubRelease.Body,
		Critical:  strings.Contains(strings.ToLower(githubRelease.Body), "critical") || strings.Contains(strings.ToLower(githubRelease.Body), "security"),
		Downloads: make(map[string]Binary),
	}

	// Map assets to downloads
	for _, asset := range githubRelease.Assets {
		platform := u.detectPlatformFromAssetName(asset.Name)
		if platform != "" {
			// For private repos, use API endpoint instead of direct download URL
			apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", u.githubRepo, asset.ID)
			release.Downloads[platform] = Binary{
				URL:  apiURL,
				Size: asset.Size,
			}
		}
	}

	// If no platform-specific assets found but there's a generic binary, use it for current platform
	if len(release.Downloads) == 0 && len(githubRelease.Assets) > 0 {
		currentPlatform := fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)

		// Use the first asset as a fallback
		asset := githubRelease.Assets[0]
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", u.githubRepo, asset.ID)
		release.Downloads[currentPlatform] = Binary{
			URL:  apiURL,
			Size: asset.Size,
		}
	}
	return release, nil
}

// detectPlatformFromAssetName detects platform from asset filename
func (u *Updater) detectPlatformFromAssetName(filename string) string {
	original := filename
	filename = strings.ToLower(filename)

	// Exact platform patterns
	platforms := map[string]string{
		"linux_amd64":   "linux_amd64",
		"linux-amd64":   "linux_amd64",
		"linux_arm64":   "linux_arm64",
		"linux-arm64":   "linux_arm64",
		"darwin_amd64":  "darwin_amd64",
		"darwin-amd64":  "darwin_amd64",
		"darwin_arm64":  "darwin_arm64",
		"darwin-arm64":  "darwin_arm64",
		"windows_amd64": "windows_amd64",
		"windows-amd64": "windows_amd64",
		"macos_amd64":   "darwin_amd64",
		"macos-amd64":   "darwin_amd64",
		"macos_arm64":   "darwin_arm64",
		"macos-arm64":   "darwin_arm64",
	}

	// Check exact patterns first
	for pattern, platform := range platforms {
		if strings.Contains(filename, pattern) {
			return platform
		}
	}

	// Fallback detection for less specific names
	if strings.Contains(filename, "linux") {
		if strings.Contains(filename, "arm64") || strings.Contains(filename, "aarch64") {
			return "linux_arm64"
		}
		return "linux_amd64"
	}

	if strings.Contains(filename, "darwin") || strings.Contains(filename, "macos") {
		if strings.Contains(filename, "arm64") || strings.Contains(filename, "aarch64") {
			return "darwin_arm64"
		}
		return "darwin_amd64"
	}

	if strings.Contains(filename, "windows") || strings.Contains(filename, ".exe") {
		if strings.Contains(filename, "arm64") {
			return "windows_arm64"
		}
		return "windows_amd64"
	}

	// If filename is just "devbox" or similar generic name, return empty
	// The caller will handle this case by using it as fallback for current platform
	if filename == "devbox" || filename == original {
		return ""
	}

	return ""
}

// performUpdate downloads and replaces the binary
func (u *Updater) performUpdate(binary *Binary) error {
	// Create temp file
	tmpFile, err := os.CreateTemp("", "devbox-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	// Download binary
	if err := u.downloadFile(binary.URL, tmpFile, binary.SHA256); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download failed: %w", err)
	}
	tmpFile.Close()

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Get current binary path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolve symlinks
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		realPath = execPath
	}

	// Try to replace binary
	if err := u.replaceBinary(tmpPath, realPath); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Update complete! The new version is now active.")
	return nil
}

// replaceBinary handles replacing the binary with proper permission handling
func (u *Updater) replaceBinary(tmpPath, realPath string) error {
	// First, try direct replacement
	if err := os.Rename(tmpPath, realPath); err == nil {
		return nil
	}

	// If rename failed, try copy
	if err := copyFile(tmpPath, realPath); err != nil {
		// If we have permission issues and this looks like a system install, try sudo
		if os.IsPermission(err) && u.isSystemInstall(realPath) {
			fmt.Printf(utils.Colorize("[AUTH]", utils.ColorBlue) + " Devbox is installed system-wide at %s\n", realPath)
			fmt.Printf("   Requesting administrator privileges to update...\n")

			return u.updateWithSudo(tmpPath, realPath)
		}
		return fmt.Errorf("permission denied updating binary at %s", realPath)
	}

	return nil
}

// isSystemInstall checks if the binary is in a system directory
func (u *Updater) isSystemInstall(path string) bool {
	systemPaths := []string{"/usr/local/bin", "/usr/bin", "/bin", "/opt"}
	for _, sysPath := range systemPaths {
		if strings.HasPrefix(path, sysPath) {
			return true
		}
	}
	return false
}

// updateWithSudo updates the binary using elevated permissions
func (u *Updater) updateWithSudo(tmpPath, realPath string) error {
	if runtime.GOOS == "windows" {
		return u.updateWithWindowsElevation(tmpPath, realPath)
	}

	// Use sudo to copy the file (Unix/Linux/macOS)
	cmd := exec.Command("sudo", "cp", tmpPath, realPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sudo update failed: %w", err)
	}

	// Make sure the binary is executable
	chmodCmd := exec.Command("sudo", "chmod", "755", realPath)
	if err := chmodCmd.Run(); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: Could not set executable permissions: %v\n", err)
	}

	return nil
}

// updateWithWindowsElevation updates the binary on Windows with UAC elevation
func (u *Updater) updateWithWindowsElevation(tmpPath, realPath string) error {
	// Create a PowerShell script to copy the file with elevation
	script := fmt.Sprintf(`
		if (-NOT ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] "Administrator")) {
			$arguments = "& { Copy-Item '%s' '%s' -Force }"
			Start-Process powershell -Verb runAs -ArgumentList $arguments -Wait
		} else {
			Copy-Item '%s' '%s' -Force
		}
	`, tmpPath, realPath, tmpPath, realPath)

	cmd := exec.Command("powershell", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Windows elevation update failed: %w", err)
	}

	return nil
}

// downloadFile downloads a file and verifies its checksum
func (u *Updater) downloadFile(url string, dest *os.File, expectedSHA256 string) error {

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}

	// Add GitHub authentication if token is available and URL is from GitHub
	if u.githubToken != "" && strings.Contains(url, "api.github.com") {
		// For GitHub API asset downloads
		req.Header.Set("Authorization", "token "+u.githubToken)
		req.Header.Set("Accept", "application/octet-stream")
		req.Header.Set("User-Agent", "devbox-updater")
	}

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	// Create hash writer
	hasher := sha256.New()
	writer := io.MultiWriter(dest, hasher)

	// Download with progress
	written, err := io.Copy(writer, resp.Body)
	if err != nil {
		return err
	}

	// Verify checksum
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if expectedSHA256 != "" && actualSHA256 != expectedSHA256 {
		return fmt.Errorf("checksum mismatch (expected %s, got %s)", expectedSHA256, actualSHA256)
	}

	fmt.Printf("Downloaded %d bytes\n", written)
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}

	// Copy permissions
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, info.Mode())
}

// compareVersions compares two semantic versions
// Returns: -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2
func compareVersions(v1, v2 string) int {
	// Strip 'v' prefix
	v1 = strings.TrimPrefix(v1, "v")
	v2 = strings.TrimPrefix(v2, "v")

	// Split versions
	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	// Compare each part
	for i := 0; i < 3; i++ {
		var p1, p2 int

		if i < len(parts1) {
			fmt.Sscanf(parts1[i], "%d", &p1)
		}
		if i < len(parts2) {
			fmt.Sscanf(parts2[i], "%d", &p2)
		}

		if p1 < p2 {
			return -1
		}
		if p1 > p2 {
			return 1
		}
	}

	return 0
}
