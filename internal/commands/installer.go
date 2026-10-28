package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aliasproject/ingot/internal/config"
	"github.com/aliasproject/ingot/internal/docker"
	"github.com/aliasproject/ingot/internal/services"
	"github.com/aliasproject/ingot/internal/ssl"
	"github.com/aliasproject/ingot/internal/utils"
)

// Installer handles the Ingot installation process
type Installer struct {
	docker   *docker.Client
	config   *config.Config
	services *services.Manager
	ssl      *ssl.Manager
}

// NewInstaller creates a new installer
func NewInstaller(dockerClient *docker.Client, cfg *config.Config, svc *services.Manager, sslMgr *ssl.Manager) *Installer {
	return &Installer{
		docker:   dockerClient,
		config:   cfg,
		services: svc,
		ssl:      sslMgr,
	}
}

// Install performs the Ingot installation. Safe to call more than once: every
// step it runs skips work that's already done (or is a harmless no-op to
// repeat), so if a previous attempt failed partway through, calling this
// again resumes from wherever it left off instead of starting over.
func (i *Installer) Install() error {
	verb := "Installing"
	if i.config.IsInstalled() {
		verb = "Resuming setup for"
	}
	fmt.Println(utils.Colorize("[STARTING]", utils.ColorCyan) + " " + verb + " Ingot Development Environment")
	fmt.Println("=" + utils.PadString("", 48))
	fmt.Println()

	// Step 1: Create directories
	fmt.Println(utils.Colorize("[DIRECTORY]", utils.ColorCyan) + " Creating directories...")
	if err := i.createDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Step 2: Configure database
	fmt.Println("\n" + utils.Colorize("[DATABASE]", utils.ColorPurple) + " Database Configuration")
	if err := i.configureDatabase(); err != nil {
		return fmt.Errorf("failed to configure database: %w", err)
	}

	// Step 3: Install binary
	fmt.Println("\n" + utils.Colorize("[PACKAGE]", utils.ColorCyan) + " Installing binary...")
	if err := i.installBinary(); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Step 4: Start global services
	fmt.Println("\n" + utils.Colorize("[WEB]", utils.ColorBlue) + " Starting global services...")
	if err := i.services.Start(); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Step 5: Setup DNS resolver
	fmt.Println("\n" + utils.Colorize("[NETWORK]", utils.ColorCyan) + " Setting up DNS resolver...")
	if err := utils.SetupTestDomainResolver(); err != nil {
		utils.PrintWarning("Failed to setup DNS resolver: " + err.Error())
	}

	// Step 6: Setup SSL certificates
	fmt.Println("\n🔒 Setting up SSL certificates...")
	if err := i.ssl.SetupSSL(); err != nil {
		utils.PrintWarning("Failed to setup SSL: " + err.Error())
	}

	// Step 7: Optional - Install CA certificate
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

	versions := []string{"8.0"}
	if dbType == "postgresql" {
		versions = []string{"16", "15", "14", "13"}
	}

	dbVersion := utils.SelectOption(fmt.Sprintf("Select %s version", selected), versions)

	// Find default port
	port := "3306"
	if dbType == "postgresql" {
		port = "5432"
	}

	// Save configuration with database type as name
	if err := i.config.AddDatabase(dbType, dbType, dbVersion, port); err != nil {
		return err
	}

	fmt.Printf("  "+utils.Colorize("[RUNNING]", utils.ColorGreen)+" Configured %s %s as '%s' database\n", selected, dbVersion, dbType)
	return nil
}

// installBinary installs the ingot binary
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
	if currentBinary == config.IngotBinary {
		fmt.Println("  Already installed at " + config.IngotBinary)
		return nil
	}

	// Try to copy directly
	if err := utils.CopyFile(currentBinary, config.IngotBinary); err != nil {
		// If copy fails, try with sudo
		fmt.Println("  Administrator privileges required")

		cmd := exec.Command("sudo", "cp", currentBinary, config.IngotBinary)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to install binary: %w", err)
		}

		// Set permissions
		exec.Command("sudo", "chmod", "755", config.IngotBinary).Run()
	} else {
		// Set permissions
		os.Chmod(config.IngotBinary, 0755)
	}

	fmt.Printf("  "+utils.Colorize("[RUNNING]", utils.ColorGreen)+" Installed to %s\n", config.IngotBinary)

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
	utils.PrintSuccess("Ingot installed successfully!")
	fmt.Println("=" + utils.PadString("", 60))
	fmt.Println()

	fmt.Println("📚 Quick Start Guide:")
	fmt.Println()
	fmt.Println("  1. Navigate to your project directory:")
	fmt.Println("     cd /path/to/your/project")
	fmt.Println()
	fmt.Println("  2. Start the project (first run sets it up):")
	fmt.Println("     ingot start")
	fmt.Println()
	fmt.Println("  4. Access your project:")
	fmt.Printf("     https://[project-name].test\n")
	fmt.Println()

	// Show service status
	fmt.Println(utils.Colorize("[WEB]", utils.ColorBlue) + " Services Status:")
	svcs, _ := i.services.Status()
	for _, svc := range svcs {
		if svc.Status == "running" {
			fmt.Printf("  "+utils.Colorize("[RUNNING]", utils.ColorGreen)+" %s: running", svc.Name)
			if len(svc.Ports) > 0 {
				fmt.Printf(" (ports: %s)", svc.Ports[0])
			}
			fmt.Println()
		}
	}

	fmt.Println()
	fmt.Println(utils.Colorize("[DOCS]", utils.ColorCyan) + " For more information:")
	fmt.Println("  ingot help         - Show all commands")
	fmt.Println("  ingot status       - Show system status")
	fmt.Println("  ingot network-help - Network configuration help")
	fmt.Println()

	// Show network info
	if hostIP, err := services.GetHostIP(); err == nil {
		fmt.Printf(utils.Colorize("[NETWORK]", utils.ColorCyan)+" Network: %s:5354 (DNS)\n", hostIP)
		fmt.Printf(utils.Colorize("[MAIL]", utils.ColorBlue)+" Mailpit: http://%s:8025\n", hostIP)
	}
}

// Uninstall performs the Ingot uninstallation
func (i *Installer) Uninstall(removeConfig bool) error {
	fmt.Println("🗑 Uninstalling Ingot...")

	// Stop all services
	fmt.Println("Stopping services...")
	i.services.Stop()

	// Remove DNS configuration
	fmt.Println("Cleaning up DNS...")
	utils.CleanupDNS()

	// Remove binary
	if utils.FileExists(config.IngotBinary) {
		fmt.Printf("Removing binary from %s...\n", config.IngotBinary)
		if err := os.Remove(config.IngotBinary); err != nil {
			// Try with sudo
			exec.Command("sudo", "rm", "-f", config.IngotBinary).Run()
		}
	}

	// No local configuration directory to remove since we use environment variables only
	if removeConfig {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Configuration is stored in environment variables and project directories only")
		fmt.Println("   To completely remove ingot configuration:")
		fmt.Println("   - Unset any INGOT_* environment variables")
	}

	utils.PrintSuccess("Ingot uninstalled")
	return nil
}
