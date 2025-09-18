package commands

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/github"
	"github.com/aliasproject/devbox/internal/utils"
)

// Updater handles Devbox updates
type Updater struct {
	config *config.Config
	github *github.Client
}

// NewUpdater creates a new updater
func NewUpdater(cfg *config.Config, gh *github.Client) *Updater {
	return &Updater{
		config: cfg,
		github: gh,
	}
}

// Update checks for updates and applies them
func (u *Updater) Update() error {
	fmt.Println("🔄 Checking for updates...")

	// Get current version
	currentVersion := config.Version
	fmt.Printf("Current version: v%s\n", currentVersion)

	// Check for latest release
	release, err := u.github.CheckForUpdates()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	// Parse version from tag
	latestVersion := strings.TrimPrefix(release.TagName, "v")

	// Compare versions
	if !u.isNewerVersion(currentVersion, latestVersion) {
		fmt.Printf("✅ Already up to date (v%s)\n", currentVersion)
		return nil
	}

	// Show update information
	fmt.Printf("\n📦 New version available: %s\n", release.TagName)
	if release.Name != "" {
		fmt.Printf("Release: %s\n", release.Name)
	}
	fmt.Printf("Published: %s\n", release.PublishedAt.Format("2006-01-02"))

	// Show release notes if available
	if release.Body != "" {
		fmt.Println("\n📝 Release Notes:")
		u.printReleaseNotes(release.Body)
	}

	// Ask for confirmation
	if !utils.PromptYesNo("\nUpdate to " + release.TagName + "?") {
		fmt.Println("Update cancelled")
		return nil
	}

	// Perform update
	return u.performUpdate(release)
}

// CheckForUpdates checks if updates are available
func (u *Updater) CheckForUpdates() (*github.ReleaseInfo, bool, error) {
	// Get current version
	currentVersion := config.Version

	// Check for latest release
	release, err := u.github.CheckForUpdates()
	if err != nil {
		return nil, false, err
	}

	// Parse version from tag
	latestVersion := strings.TrimPrefix(release.TagName, "v")

	// Check if newer
	isNewer := u.isNewerVersion(currentVersion, latestVersion)

	return release, isNewer, nil
}

// performUpdate performs the actual update
func (u *Updater) performUpdate(release *github.ReleaseInfo) error {
	fmt.Println("\n🚀 Updating Devbox...")

	// Construct download URL based on OS and architecture
	downloadURL, err := u.getDownloadURL(release)
	if err != nil {
		return err
	}

	if downloadURL == "" {
		// No direct download available, show manual instructions
		return u.showManualUpdateInstructions(release)
	}

	// Download new binary
	fmt.Printf("Downloading from: %s\n", downloadURL)
	// Implementation would download and replace binary
	// For now, show manual instructions
	return u.showManualUpdateInstructions(release)
}

// getDownloadURL constructs the download URL for the current platform
func (u *Updater) getDownloadURL(release *github.ReleaseInfo) (string, error) {
	// This would construct the appropriate download URL based on the platform
	// For now, return empty to trigger manual instructions
	return "", nil
}

// showManualUpdateInstructions displays manual update instructions
func (u *Updater) showManualUpdateInstructions(release *github.ReleaseInfo) error {
	fmt.Println("\n📋 Manual Update Instructions:")
	fmt.Println(strings.Repeat("-", 50))

	fmt.Printf("\n1. Visit the release page:\n")
	fmt.Printf("   %s\n", release.HTMLURL)

	fmt.Printf("\n2. Download the appropriate binary for your system:\n")
	fmt.Printf("   OS: %s\n", runtime.GOOS)
	fmt.Printf("   Architecture: %s\n", runtime.GOARCH)

	fmt.Printf("\n3. Replace the existing binary:\n")
	fmt.Printf("   %s\n", config.DevboxBinary)

	fmt.Printf("\n4. Verify the update:\n")
	fmt.Printf("   devbox --version\n")

	fmt.Println("\n" + strings.Repeat("-", 50))

	// Open the release page if possible
	if utils.PromptYesNo("\nOpen release page in browser?") {
		utils.OpenURL(release.HTMLURL)
	}

	return nil
}

// isNewerVersion compares two version strings
func (u *Updater) isNewerVersion(current, latest string) bool {
	// Remove 'v' prefix if present
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	// Parse semantic versions
	currentParts := u.parseVersion(current)
	latestParts := u.parseVersion(latest)

	// Compare major.minor.patch
	for i := 0; i < 3 && i < len(currentParts) && i < len(latestParts); i++ {
		if latestParts[i] > currentParts[i] {
			return true
		} else if latestParts[i] < currentParts[i] {
			return false
		}
	}

	// If all parts are equal, check if latest has more parts
	return len(latestParts) > len(currentParts)
}

// parseVersion parses a semantic version string
func (u *Updater) parseVersion(version string) []int {
	parts := strings.Split(version, ".")
	result := make([]int, 0, len(parts))

	for _, part := range parts {
		// Handle pre-release versions (e.g., "1.0.0-beta")
		if idx := strings.IndexAny(part, "-+"); idx != -1 {
			part = part[:idx]
		}

		var num int
		fmt.Sscanf(part, "%d", &num)
		result = append(result, num)
	}

	// Ensure at least 3 parts (major.minor.patch)
	for len(result) < 3 {
		result = append(result, 0)
	}

	return result
}

// printReleaseNotes formats and prints release notes
func (u *Updater) printReleaseNotes(body string) {
	lines := strings.Split(body, "\n")
	maxWidth := 70

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			fmt.Println()
			continue
		}

		// Handle headers
		if strings.HasPrefix(line, "#") {
			fmt.Println()
			fmt.Println(line)
			continue
		}

		// Handle list items
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			u.printWrapped("  "+line, maxWidth)
			continue
		}

		// Regular text
		u.printWrapped("  "+line, maxWidth)
	}
}

// printWrapped prints text wrapped at the specified width
func (u *Updater) printWrapped(text string, width int) {
	words := strings.Fields(text)
	if len(words) == 0 {
		return
	}

	currentLine := ""
	indent := ""

	// Detect and preserve indentation
	for i, char := range text {
		if char != ' ' && char != '\t' {
			indent = text[:i]
			break
		}
	}

	for _, word := range words {
		if currentLine == "" {
			currentLine = indent + word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			fmt.Println(currentLine)
			currentLine = indent + word
		}
	}

	if currentLine != "" {
		fmt.Println(currentLine)
	}
}

// AutoUpdateCheck performs an automatic update check (non-interactive)
func (u *Updater) AutoUpdateCheck() {
	// Check if auto-update checks are enabled
	if !u.shouldCheckForUpdates() {
		return
	}

	// Check for updates silently
	release, isNewer, err := u.CheckForUpdates()
	if err != nil || !isNewer {
		return
	}

	// Show notification
	fmt.Println()
	utils.PrintInfo(fmt.Sprintf("Update available: %s (current: v%s)", release.TagName, config.Version))
	fmt.Println("Run 'devbox update' to update")
	fmt.Println()
}

// shouldCheckForUpdates determines if we should check for updates
func (u *Updater) shouldCheckForUpdates() bool {
	// Could implement logic to check periodically (e.g., once per day)
	// For now, return false to avoid checking on every command
	return false
}
