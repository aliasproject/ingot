package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
	"github.com/aliasproject/devbox/internal/github"
	"github.com/aliasproject/devbox/internal/services"
	"github.com/aliasproject/devbox/internal/ssl"
	"github.com/aliasproject/devbox/internal/utils"
)

// Installer handles the Devbox installation process
type Installer struct {
	docker   *docker.Client
	config   *config.Config
	services *services.Manager
	ssl      *ssl.Manager
	github   *github.Client
}

// NewInstaller creates a new installer
func NewInstaller(dockerClient *docker.Client, cfg *config.Config, svc *services.Manager, sslMgr *ssl.Manager, gh *github.Client) *Installer {
	return &Installer{
		docker:   dockerClient,
		config:   cfg,
		services: svc,
		ssl:      sslMgr,
		github:   gh,
	}
}

// Install performs the Devbox installation
func (i *Installer) Install() error {
	fmt.Println("🚀 Installing Devbox Development Environment")
	fmt.Println("=" + utils.PadString("", 48))
	fmt.Println()

	// Step 1: Create directories
	fmt.Println("📁 Creating directories...")
	if err := i.createDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Step 2: Configure database
	fmt.Println("\n💾 Database Configuration")
	if err := i.configureDatabase(); err != nil {
		return fmt.Errorf("failed to configure database: %w", err)
	}

	// Step 3: Setup GitHub authentication
	fmt.Println("\n🔑 GitHub Authentication")
	if err := i.setupGitHubAuth(); err != nil {
		// Non-fatal error
		utils.PrintWarning("GitHub authentication skipped: " + err.Error())
	}

	// Step 4: Install binary
	fmt.Println("\n📦 Installing binary...")
	if err := i.installBinary(); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Step 5: Start global services
	fmt.Println("\n🌐 Starting global services...")
	if err := i.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Step 6: Setup DNS resolver (macOS only)
	fmt.Println("\n🔗 Setting up DNS resolver...")
	if err := utils.SetupTestDomainResolver(); err != nil {
		utils.PrintWarning("Failed to setup DNS resolver: " + err.Error())
	}

	// Step 7: Setup SSL certificates
	fmt.Println("\n🔒 Setting up SSL certificates...")
	if err := i.ssl.SetupSSL(); err != nil {
		utils.PrintWarning("Failed to setup SSL: " + err.Error())
	}

	// Step 8: Optional - Install CA certificate
	if utils.PromptYesNo("\nInstall CA certificate to system?") {
		if err := i.ssl.InstallCertificate(); err != nil {
			utils.PrintWarning("Failed to install certificate: " + err.Error())
		}
	}

	// Save configuration
	if err := i.config.Save(); err != nil {
		utils.PrintWarning("Failed to save configuration: " + err.Error())
	}

	// Show success message
	i.showSuccessMessage()

	return nil
}

// createDirectories creates the necessary directories (no-op since we don't use local directories)
func (i *Installer) createDirectories() error {
	// No local directories to create anymore - everything uses environment variables
	// and project-local configuration files
	return nil
}

// configureDatabase asks user about database preferences
func (i *Installer) configureDatabase() error {
	if !utils.PromptYesNo("Would you like to include a database service?") {
		fmt.Println("  Database service skipped")
		return nil
	}

	// Show proper capitalization
	dbTypeOptions := []string{"MySQL", "PostgreSQL"}
	selected := utils.SelectOption("Select database type", dbTypeOptions)

	dbType := "mysql"
	if selected == "PostgreSQL" {
		dbType = "postgresql"
	}

	versions := []string{"8.0", "5.7"}
	if dbType == "postgresql" {
		versions = []string{"16", "15", "14", "13"}
	}

	dbVersion := utils.SelectOption(fmt.Sprintf("Select %s version", selected), versions)

	// Find default port
	port := "3306"
	if dbType == "postgresql" {
		port = "5432"
	}

	// Save configuration with default name
	if err := i.config.AddDatabase("main", dbType, dbVersion, port); err != nil {
		return err
	}

	fmt.Printf("  ✅ Configured %s %s as 'main' database\n", selected, dbVersion)
	return nil
}

// setupGitHubAuth handles GitHub authentication setup
func (i *Installer) setupGitHubAuth() error {
	fmt.Println("GitHub authentication enables access to private container images")

	if !utils.PromptYesNo("Setup GitHub authentication now?") {
		return fmt.Errorf("skipped by user")
	}

	// Try to get existing token
	if err := i.github.EnsureAuthenticated(); err == nil {
		fmt.Println("  ✅ Already authenticated")
		return nil
	}

	// Perform authentication
	return i.github.Authenticate()
}

// installBinary installs the devbox binary
func (i *Installer) installBinary() error {
	// Get current executable path
	currentBinary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current binary path: %w", err)
	}

	// Resolve symlinks
	currentBinary, err = filepath.EvalSymlinks(currentBinary)
	if err != nil {
		return fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Check if already installed at target location
	if currentBinary == config.DevboxBinary {
		fmt.Println("  Already installed at " + config.DevboxBinary)
		return nil
	}

	// Try to copy directly
	if err := utils.CopyFile(currentBinary, config.DevboxBinary); err != nil {
		// If copy fails, try with sudo
		fmt.Println("  Administrator privileges required")

		cmd := exec.Command("sudo", "cp", currentBinary, config.DevboxBinary)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install binary: %w", err)
		}

		// Set permissions
		exec.Command("sudo", "chmod", "755", config.DevboxBinary).Run()
	} else {
		// Set permissions
		os.Chmod(config.DevboxBinary, 0755)
	}

	fmt.Printf("  ✅ Installed to %s\n", config.DevboxBinary)

	// Check if in PATH
	if !i.isInPath() {
		utils.PrintWarning("Binary installed but /usr/local/bin is not in your PATH")
		fmt.Println("  Add to your shell configuration:")
		fmt.Println("  export PATH=\"/usr/local/bin:$PATH\"")
	}

	return nil
}

// isInPath checks if /usr/local/bin is in PATH
func (i *Installer) isInPath() bool {
	path := os.Getenv("PATH")
	return utils.StringSliceContains(filepath.SplitList(path), "/usr/local/bin")
}

// showSuccessMessage displays installation success message
func (i *Installer) showSuccessMessage() {
	fmt.Println()
	fmt.Println("=" + utils.PadString("", 60))
	utils.PrintSuccess("Devbox installed successfully!")
	fmt.Println("=" + utils.PadString("", 60))
	fmt.Println()

	fmt.Println("📚 Quick Start Guide:")
	fmt.Println()
	fmt.Println("  1. Navigate to your project directory:")
	fmt.Println("     cd /path/to/your/project")
	fmt.Println()
	fmt.Println("  2. Initialize a new project:")
	fmt.Println("     devbox init")
	fmt.Println()
	fmt.Println("  3. Start the project:")
	fmt.Println("     devbox start")
	fmt.Println()
	fmt.Println("  4. Access your project:")
	fmt.Printf("     https://[project-name].test\n")
	fmt.Println()

	// Show service status
	fmt.Println("🌐 Services Status:")
	svcs, _ := i.services.Status()
	for _, svc := range svcs {
		if svc.Status == "running" {
			fmt.Printf("  ✅ %s: running", svc.Name)
			if len(svc.Ports) > 0 {
				fmt.Printf(" (ports: %s)", svc.Ports[0])
			}
			fmt.Println()
		}
	}

	fmt.Println()
	fmt.Println("📖 For more information:")
	fmt.Println("  devbox help         - Show all commands")
	fmt.Println("  devbox status       - Show system status")
	fmt.Println("  devbox network-help - Network configuration help")
	fmt.Println()

	// Show network info
	if hostIP, err := services.GetHostIP(); err == nil {
		fmt.Printf("🔗 Network: %s:5354 (DNS)\n", hostIP)
		fmt.Printf("📧 MailHog: http://%s:8025\n", hostIP)
	}
}

// Uninstall performs the Devbox uninstallation
func (i *Installer) Uninstall(removeConfig bool) error {
	fmt.Println("🗑 Uninstalling Devbox...")

	// Stop all services
	fmt.Println("Stopping services...")
	i.services.Stop()

	// Remove DNS configuration
	fmt.Println("Cleaning up DNS...")
	utils.CleanupDNS()

	// Remove binary
	if utils.FileExists(config.DevboxBinary) {
		fmt.Printf("Removing binary from %s...\n", config.DevboxBinary)
		if err := os.Remove(config.DevboxBinary); err != nil {
			// Try with sudo
			exec.Command("sudo", "rm", "-f", config.DevboxBinary).Run()
		}
	}

	// No local configuration directory to remove since we use environment variables only
	if removeConfig {
		fmt.Println("💡 Configuration is stored in environment variables and project directories only")
		fmt.Println("   To completely remove devbox configuration:")
		fmt.Println("   - Unset GITHUB_TOKEN environment variable")
		fmt.Println("   - Remove .devbox.json files from project directories")
	}

	utils.PrintSuccess("Devbox uninstalled")
	return nil
}
