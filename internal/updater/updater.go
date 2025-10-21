package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
			fmt.Printf("Current version: v%s\n", config.Version)
			fmt.Printf("Latest version: v%s\n", release.Version)
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
	fmt.Printf("Current version: v%s\n", config.Version)
	fmt.Printf("New version:     v%s\n", release.Version)
	if release.Critical {
		fmt.Println("⚠️  This is a critical security update")
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
			release.Downloads[platform] = Binary{
				URL:  asset.BrowserDownloadURL,
				Size: asset.Size,
				// Note: GitHub doesn't provide SHA256 in API, would need to be in release notes or separate
			}
		}
	}

	return release, nil
}

// detectPlatformFromAssetName detects platform from asset filename
func (u *Updater) detectPlatformFromAssetName(filename string) string {
	filename = strings.ToLower(filename)

	// Common patterns for different platforms
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
	}

	for pattern, platform := range platforms {
		if strings.Contains(filename, pattern) {
			return platform
		}
	}

	// Fallback detection
	if strings.Contains(filename, "linux") {
		if strings.Contains(filename, "arm64") || strings.Contains(filename, "aarch64") {
			return "linux_arm64"
		}
		return "linux_amd64"
	}
	if strings.Contains(filename, "darwin") || strings.Contains(filename, "macos") {
		if strings.Contains(filename, "arm64") {
			return "darwin_arm64"
		}
		return "darwin_amd64"
	}
	if strings.Contains(filename, "windows") || strings.Contains(filename, ".exe") {
		return "windows_amd64"
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

	// Create backup
	backupPath := realPath + ".backup"
	if err := copyFile(realPath, backupPath); err != nil {
		return fmt.Errorf("failed to create backup: %w", err)
	}

	// Replace binary (atomic on Unix systems)
	if err := os.Rename(tmpPath, realPath); err != nil {
		// Try copy if rename fails (Windows or cross-device)
		if err := copyFile(tmpPath, realPath); err != nil {
			// Restore backup
			copyFile(backupPath, realPath)
			return fmt.Errorf("failed to replace binary: %w", err)
		}
	}

	// Clean up backup
	os.Remove(backupPath)

	fmt.Println("✅ Update complete! Please restart devbox.")
	return nil
}

// downloadFile downloads a file and verifies its checksum
func (u *Updater) downloadFile(url string, dest *os.File, expectedSHA256 string) error {
	resp, err := u.httpClient.Get(url)
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
