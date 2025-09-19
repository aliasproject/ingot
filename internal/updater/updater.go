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

// Updater handles self-updates
type Updater struct {
	manifestURL string
	httpClient  *http.Client
}

// New creates a new updater instance
func New() *Updater {
	// Get manifest URL from environment or use default
	manifestURL := os.Getenv("DEVBOX_UPDATE_URL")
	if manifestURL == "" {
		manifestURL = "https://devbox-updates.s3.amazonaws.com/stable/manifest.json"
	}

	return &Updater{
		manifestURL: manifestURL,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}
}

// CheckForUpdate checks if a new version is available
func (u *Updater) CheckForUpdate() (*Release, bool, error) {
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

// fetchLatestRelease fetches the latest release information
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
