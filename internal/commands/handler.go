package commands

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
	"github.com/aliasproject/devbox/internal/github"
	"github.com/aliasproject/devbox/internal/project"
	"github.com/aliasproject/devbox/internal/services"
	"github.com/aliasproject/devbox/internal/ssl"
	"github.com/aliasproject/devbox/internal/updater"
	"github.com/aliasproject/devbox/internal/utils"
)

// Handler manages command execution
type Handler struct {
	docker   *docker.Client
	config   *config.Config
	projects *project.Manager
	services *services.Manager
	ssl      *ssl.Manager
	github   *github.Client
	updater  *updater.Updater
}

// NewHandler creates a new command handler
func NewHandler(dockerClient *docker.Client, cfg *config.Config) *Handler {
	return &Handler{
		docker:   dockerClient,
		config:   cfg,
		projects: project.NewManager(dockerClient),
		services: services.NewManager(dockerClient),
		ssl:      ssl.NewManager(dockerClient, cfg),
		github:   github.NewClient(cfg),
		updater:  updater.New(),
	}
}

// GetServices returns the services manager
func (h *Handler) GetServices() *services.Manager {
	return h.services
}

// GetSSL returns the SSL manager
func (h *Handler) GetSSL() *ssl.Manager {
	return h.ssl
}

// GetGitHub returns the GitHub client
func (h *Handler) GetGitHub() *github.Client {
	return h.github
}

// Execute processes the command line arguments and executes the appropriate command
func (h *Handler) Execute(args []string) error {
	if len(args) < 2 {
		return h.handleDefaultCommand()
	}

	command := args[1]
	commandArgs := args[2:]

	// Handle compound commands
	switch command {
	case "global":
		return h.handleGlobalCommand(commandArgs)
	case "database":
		return h.handleDatabaseCommand(commandArgs)
	case "cert":
		return h.handleCertCommand(commandArgs)
	}

	// Handle single commands
	switch command {
	// Help and version
	case "help", "-h", "--help":
		return h.showHelp()
	case "version", "-v", "--version":
		return h.showVersion()

	// Installation and updates
	case "install":
		return h.handleInstall()
	case "update":
		return h.handleUpdate()
	case "check-update":
		return h.handleCheckUpdate()
	case "uninstall":
		return h.handleUninstall()

	// Project management
	case "init":
		return h.handleInit(commandArgs)
	case "start":
		return h.handleStart(commandArgs...)
	case "stop":
		return h.handleStop()
	case "restart":
		return h.handleRestart()
	case "remove", "rm":
		return h.handleRemove()
	case "status":
		return h.handleStatus()
	case "list", "ls":
		return h.handleList()
	case "php", "php-version":
		return h.handlePHPVersion(commandArgs)
	case "server-root", "root":
		return h.handleServerRoot(commandArgs)

	// Container interaction
	case "exec", "run":
		return h.handleExec(commandArgs)
	case "shell", "sh", "bash":
		return h.handleShell()

	// Network and DNS
	case "network-help":
		return h.handleNetworkHelp()
	case "dns-cleanup":
		return h.handleDNSCleanup()

	// GitHub authentication
	case "github-auth", "auth":
		return h.handleGitHubAuth()

	// Logs
	case "logs":
		return h.handleLogs(commandArgs)

	default:
		// Try to execute command in container if project is initialized
		return h.handleContainerExec(command, commandArgs)
	}
}

// handleDefaultCommand shows status or help when no command is specified
func (h *Handler) handleDefaultCommand() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status != "not initialized" {
		return h.showProjectStatus(project)
	}

	fmt.Println("💡 No devbox project found in this directory")
	fmt.Println("Run 'devbox init' to initialize a project or 'devbox help' for more information")
	return nil
}

// handleGlobalCommand handles global service commands
func (h *Handler) handleGlobalCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing global subcommand. Use: global [start|stop|restart|status]")
	}

	switch args[0] {
	case "start":
		fmt.Println("🚀 Starting global services...")
		if err := h.services.Start(); err != nil {
			return fmt.Errorf("failed to start services: %w", err)
		}
		fmt.Println("✅ Global services started successfully")
		return h.showServicesStatus()

	case "stop":
		fmt.Println("🛑 Stopping global services...")
		if err := h.services.Stop(); err != nil {
			return fmt.Errorf("failed to stop services: %w", err)
		}
		fmt.Println("✅ Global services stopped")
		return nil

	case "restart":
		fmt.Println("🔄 Restarting global services...")
		if err := h.services.Restart(); err != nil {
			return fmt.Errorf("failed to restart services: %w", err)
		}
		fmt.Println("✅ Global services restarted")
		return h.showServicesStatus()

	case "status":
		return h.showServicesStatus()

	default:
		return fmt.Errorf("unknown global command: %s", args[0])
	}
}

// handleDatabaseCommand handles database commands
func (h *Handler) handleDatabaseCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing database subcommand. Use: database [add|remove|list]")
	}

	switch args[0] {
	case "add":
		return h.handleDatabaseAdd()

	case "remove", "rm":
		return h.handleDatabaseRemove()

	case "list", "ls":
		return h.handleDatabaseList()

	default:
		return fmt.Errorf("unknown database command: %s", args[0])
	}
}

// handleCertCommand handles certificate commands
func (h *Handler) handleCertCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing cert subcommand. Use: cert [install|verify|debug|remove]")
	}

	switch args[0] {
	case "install":
		return h.ssl.InstallCertificate()

	case "verify":
		return h.ssl.VerifyCertificate()

	case "debug":
		return h.ssl.DebugCertificate()

	case "generate":
		if len(args) < 2 {
			return fmt.Errorf("missing domain for certificate generation")
		}
		return h.ssl.GenerateCertificate(args[1])

	case "remove":
		proj, err := h.projects.GetCurrentProject()
		if err != nil {
			return err
		}
		if proj.Status == "not initialized" {
			return fmt.Errorf("project not initialized")
		}

		fmt.Printf("🔒 Removing SSL certificate for project '%s'...\n", proj.Name)
		if err := h.ssl.RemoveProjectSSL(proj.Name); err != nil {
			return fmt.Errorf("failed to remove SSL certificate: %w", err)
		}

		fmt.Printf("✅ SSL certificate removed. Project now accessible via HTTP at: http://%s.test\n", proj.Name)
		return nil

	default:
		return fmt.Errorf("unknown cert command: %s", args[0])
	}
}

// handleInstall installs Devbox
func (h *Handler) handleInstall() error {
	if h.config.IsInstalled() {
		fmt.Printf("✅ Devbox is already installed (v%s)\n", config.Version)
		return h.showStatus()
	}

	installer := NewInstaller(h.docker, h.config, h.services, h.ssl, h.github)
	return installer.Install()
}

// handleUpdate updates Devbox to the latest version
func (h *Handler) handleUpdate() error {
	// Set GitHub token if available for private repo access
	if token := h.config.GetGithubToken(); token != "" {
		h.updater.SetGitHubToken(token)
	} else {
		// Try to ensure GitHub authentication for update checks
		fmt.Println("🔍 Checking for updates from private repository...")
		if err := h.github.EnsureAuthenticated(); err != nil {
			fmt.Printf("⚠️  Warning: No GitHub authentication available for private repo updates.\n")
			fmt.Printf("   Run 'devbox github-auth' to authenticate for access to latest releases.\n")
			fmt.Printf("   Falling back to public update manifest...\n\n")
		} else {
			// Authentication successful, set token
			if token := h.config.GetGithubToken(); token != "" {
				h.updater.SetGitHubToken(token)
				fmt.Println("✅ GitHub authentication found, checking private repository...")
			}
		}
	}

	return h.updater.Update(false)
}

// handleCheckUpdate checks for available updates without installing
func (h *Handler) handleCheckUpdate() error {
	// Set GitHub token if available for private repo access
	if token := h.config.GetGithubToken(); token != "" {
		h.updater.SetGitHubToken(token)
	} else {
		// Try to ensure GitHub authentication for update checks
		fmt.Println("🔍 Checking for updates from private repository...")
		if err := h.github.EnsureAuthenticated(); err != nil {
			fmt.Printf("⚠️  Warning: No GitHub authentication available for private repo updates.\n")
			fmt.Printf("   Run 'devbox github-auth' to authenticate for access to latest releases.\n")
			fmt.Printf("   Falling back to public update manifest...\n\n")
		} else {
			// Authentication successful, set token
			if token := h.config.GetGithubToken(); token != "" {
				h.updater.SetGitHubToken(token)
				fmt.Println("✅ GitHub authentication found, checking private repository...")
			}
		}
	}

	// Check for updates
	release, isNewer, err := h.updater.CheckForUpdate()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("Current version: v%s\n", config.Version)

	if !isNewer {
		fmt.Println("✅ You are running the latest version")
		return nil
	}

	fmt.Printf("New version available: v%s\n", release.Version)
	if release.Critical {
		fmt.Println("⚠️  This is a critical security update")
	}
	if release.Notes != "" {
		fmt.Printf("\nRelease notes:\n%s\n", release.Notes)
	}
	fmt.Printf("\nRun 'devbox update' to install the latest version.\n")

	return nil
}

// handleUninstall uninstalls Devbox
func (h *Handler) handleUninstall() error {
	if !utils.PromptYesNo("Are you sure you want to uninstall Devbox?") {
		fmt.Println("Uninstall cancelled")
		return nil
	}

	fmt.Println("🗑 Uninstalling Devbox...")

	// Stop all services
	h.services.Stop()

	// Remove all projects
	projects, _ := h.projects.List()
	for _, proj := range projects {
		fmt.Printf("Removing project %s...\n", proj.Name)
		h.projects.Remove(true)
	}

	// Remove DNS configuration
	utils.CleanupDNS()

	// Remove binary
	os.Remove(config.DevboxBinary)

	// Remove configuration directory
	if utils.PromptYesNo("Remove configuration and certificates?") {
		os.RemoveAll(h.config.DevboxDir)
	}

	fmt.Println("✅ Devbox uninstalled")
	return nil
}

// handleInit initializes a new project
func (h *Handler) handleInit(args []string) error {
	// Init is now just an alias for start with PHP version selection
	fmt.Println("ℹ️  'devbox init' is deprecated. Using 'devbox start' instead...")
	return h.handleStart(args...)
}

// handleStart starts the current project
func (h *Handler) handleStart(args ...string) error {
	// Get project info early for cleanup purposes
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	// Select PHP version if not specified
	phpVersion := h.config.DefaultPHPVersion
	if len(args) > 0 && project.ValidatePHPVersion(args[0]) {
		phpVersion = args[0]
	} else if proj.Status == "not initialized" {
		// Only prompt for PHP version on first start
		phpVersion = utils.SelectOption("Select PHP Version", project.GetAvailablePHPVersions())
	}

	// Collect server root path (default to "/app")
	serverRoot := "/app"
	if proj.Status == "not initialized" {
		// Only prompt for server root on first start
		input := utils.GetInput("Enter server root path (default: /app): ")
		if input != "" {
			serverRoot = input
		}
	}

	// Setup cleanup function that will be called on any failure
	cleanupOnFailure := func(reason string) {
		fmt.Printf("🧹 Cleaning up after failure: %s\n", reason)
		containerName := fmt.Sprintf("devbox_php_%s", proj.Name)

		// Try to stop the container first
		if stopErr := h.docker.Run("stop", containerName); stopErr != nil {
			// Don't log if container doesn't exist
			if !strings.Contains(stopErr.Error(), "No such container") {
				fmt.Printf("⚠️  Warning: Failed to stop container: %v\n", stopErr)
			}
		}

		// Force remove the container
		if removeErr := h.docker.Run("rm", "-f", containerName); removeErr != nil {
			// Don't log if container doesn't exist
			if !strings.Contains(removeErr.Error(), "No such container") {
				fmt.Printf("⚠️  Warning: Failed to remove container: %v\n", removeErr)
			}
		}
	}

	// Ensure global services are running
	if err := h.services.EnsureRunning(); err != nil {
		return fmt.Errorf("failed to start global services: %w", err)
	}

	// Ensure GitHub authentication
	if err := h.github.EnsureAuthenticated(); err != nil {
		fmt.Printf("⚠️  Warning: GitHub authentication failed: %v\n", err)
	}

	fmt.Printf("🚀 Starting project '%s' with PHP %s...\n", proj.Name, phpVersion)

	if err := h.projects.Start(phpVersion, serverRoot); err != nil {
		cleanupOnFailure("project start failed")
		return fmt.Errorf("failed to start project: %w", err)
	}

	// Setup SSL for project
	if h.config.EnableSSL {
		if err := h.ssl.SetupProjectSSL(proj.Name); err != nil {
			// SSL setup failed - cleanup and return error
			fmt.Printf("❌ Failed to setup SSL certificate: %v\n", err)
			cleanupOnFailure("SSL certificate setup failed")

			return fmt.Errorf("project startup failed due to SSL certificate error. Please check:\n"+
				"  1. Nginx container is running (devbox global status)\n"+
				"  2. CA certificate exists (devbox cert verify)\n"+
				"  3. Try running: devbox global restart\n"+
				"Error details: %w", err)
		}
	}

	// Refresh project status after successful start
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		cleanupOnFailure("failed to get project status after start")
		return fmt.Errorf("failed to get project status: %w", err)
	}

	// Only show success message if we have valid project info
	if project.Status == "running" && project.URL != "" {
		fmt.Printf("✅ Project started successfully with PHP %s\n", phpVersion)
		fmt.Printf("🌐 Access your project at: %s\n", project.URL)
		return h.showProjectStatus(project)
	}

	// Something went wrong if we get here
	cleanupOnFailure("project started but status is unclear")
	return fmt.Errorf("project started but status is unclear. Run 'devbox status' to check")
}

// handleStop stops the current project
func (h *Handler) handleStop() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	fmt.Printf("🛑 Stopping project '%s'...\n", project.Name)

	if err := h.projects.Stop(); err != nil {
		return fmt.Errorf("failed to stop project: %w", err)
	}

	fmt.Println("✅ Project stopped")
	return nil
}

// handleRestart restarts the current project
func (h *Handler) handleRestart() error {
	if err := h.handleStop(); err != nil {
		return err
	}
	return h.handleStart()
}

// handleRemove removes the current project
func (h *Handler) handleRemove() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("no project to remove")
	}

	if !utils.PromptYesNo(fmt.Sprintf("Remove project '%s'?", project.Name)) {
		fmt.Println("Remove cancelled")
		return nil
	}

	fmt.Printf("🗑 Removing project '%s'...\n", project.Name)

	// Remove SSL certificates and vhost configuration
	fmt.Println("🔒 Cleaning up SSL certificates...")
	if err := h.ssl.RemoveProjectSSL(project.Name); err != nil {
		fmt.Printf("⚠️  Warning: Failed to clean up SSL certificates: %v\n", err)
	}

	fmt.Println("🌐 Removing vhost configuration...")
	if err := h.ssl.RemoveProjectVhost(project.Name); err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove vhost configuration: %v\n", err)
	}

	// Remove project
	if err := h.projects.Remove(true); err != nil {
		return fmt.Errorf("failed to remove project: %w", err)
	}

	fmt.Println("✅ Project removed")
	return nil
}

// handleStatus shows the current status
func (h *Handler) handleStatus() error {
	return h.showStatus()
}

// handleList lists all projects
func (h *Handler) handleList() error {
	projects, err := h.projects.List()
	if err != nil {
		return fmt.Errorf("failed to list projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("No projects found")
		return nil
	}

	fmt.Printf("Found %d project(s):\n\n", len(projects))
	for _, proj := range projects {
		status := "🔴 stopped"
		if proj.Status == "running" {
			status = "✅ running"
		}

		fmt.Printf("  %s (%s)\n", proj.Name, status)
		fmt.Printf("    PHP: %s\n", proj.PHPVersion)
		fmt.Printf("    Path: %s\n", proj.Path)
		if proj.Status == "running" {
			fmt.Printf("    URL: %s\n", proj.URL)
		}
		fmt.Println()
	}

	return nil
}

// handlePHPVersion changes PHP version for the current project
func (h *Handler) handlePHPVersion(args []string) error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'devbox init' first")
	}

	fmt.Printf("Current PHP version: %s\n", proj.PHPVersion)

	// Select new version
	newVersion := ""
	if len(args) > 0 && project.ValidatePHPVersion(args[0]) {
		newVersion = args[0]
	} else {
		newVersion = utils.SelectOption("Select new PHP version", project.GetAvailablePHPVersions())
	}

	if newVersion == proj.PHPVersion {
		fmt.Printf("✅ Already using PHP %s\n", newVersion)
		return nil
	}

	fmt.Printf("🔄 Changing PHP version to %s...\n", newVersion)

	if err := h.projects.ChangePHPVersion(newVersion); err != nil {
		return fmt.Errorf("failed to change PHP version: %w", err)
	}

	fmt.Printf("✅ PHP version changed to %s\n", newVersion)
	fmt.Println("Run 'devbox start' to start with the new version")
	return nil
}

// handleServerRoot changes server root for the current project
func (h *Handler) handleServerRoot(args []string) error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'devbox init' first")
	}

	// Get current server root from config
	currentServerRoot := proj.ServerRoot
	if currentServerRoot == "" {
		currentServerRoot = "public" // default
	}

	fmt.Printf("Current server root: %s\n", currentServerRoot)

	// Get new server root
	newServerRoot := ""
	if len(args) > 0 && args[0] != "" {
		newServerRoot = args[0]
	} else {
		input := utils.GetInput("Enter new server root path: ")
		if input == "" {
			fmt.Println("❌ Server root cannot be empty")
			return nil
		}
		newServerRoot = input
	}

	if newServerRoot == currentServerRoot {
		fmt.Printf("✅ Already using server root: %s\n", newServerRoot)
		return nil
	}

	fmt.Printf("🔄 Changing server root to %s...\n", newServerRoot)

	if err := h.projects.ChangeServerRoot(newServerRoot); err != nil {
		return fmt.Errorf("failed to change server root: %w", err)
	}

	fmt.Printf("✅ Server root changed to %s\n", newServerRoot)
	if proj.Status == "running" {
		fmt.Println("🔄 Restarting container to apply changes...")
		if err := h.handleRestart(); err != nil {
			return fmt.Errorf("failed to restart container: %w", err)
		}
	} else {
		fmt.Println("Run 'devbox start' to start with the new server root")
	}
	return nil
}

// handleContainerExec executes a command in the project container (for backward compatibility)
func (h *Handler) handleContainerExec(command string, args []string) error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("unknown command: %s (project not initialized)", command)
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running. Run 'devbox start' first")
	}

	// Prepare command for execution
	execCommand := append([]string{command}, args...)
	err = h.projects.Exec(execCommand, true)
	if err != nil {
		// If the error indicates the command wasn't found, provide a helpful message
		if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("'%s' is not a devbox command or available in the container\nTry 'devbox help' for available commands or 'devbox shell' to explore the container", command)
		}
	}
	return err
}

// handleExec explicitly executes a command in the project container
func (h *Handler) handleExec(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command specified. Usage: devbox exec <command> [args...]")
	}

	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'devbox start' first")
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running. Run 'devbox start' first")
	}

	err = h.projects.Exec(args, true)
	if err != nil {
		// The project manager already provides a good error message
		return err
	}
	return nil
}

// handleShell opens an interactive shell in the project container
func (h *Handler) handleShell() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'devbox start' first")
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running. Run 'devbox start' first")
	}

	fmt.Printf("📂 Opening shell in %s container...\n", project.Name)

	// Open an interactive shell, trying multiple shell types
	return h.projects.OpenShell()
}

// handleLogs shows container logs
func (h *Handler) handleLogs(args []string) error {
	if len(args) == 0 {
		// Show project logs
		project, err := h.projects.GetCurrentProject()
		if err != nil {
			return err
		}

		if project.Status == "not initialized" {
			return fmt.Errorf("project not initialized")
		}

		return h.docker.StreamOutput(
			func(line string) { fmt.Println(line) },
			"logs", "-f", project.Container,
		)
	}

	// Show specific container logs
	container := args[0]
	if !strings.HasPrefix(container, "devbox_") {
		container = "devbox_" + container
	}

	return h.docker.StreamOutput(
		func(line string) { fmt.Println(line) },
		"logs", "-f", container,
	)
}

// handleNetworkHelp shows network configuration help
func (h *Handler) handleNetworkHelp() error {
	hostIP, err := services.GetHostIP()
	if err != nil {
		fmt.Println("Could not determine host IP address")
		return nil
	}

	fmt.Printf("Host IP: %s\n\n", hostIP)
	fmt.Println("NETWORK ACCESS OPTIONS:")
	fmt.Println("\n1. Router DNS Configuration (Recommended):")
	fmt.Printf("   Configure your router to forward *.test domains to %s:5354\n", hostIP)
	fmt.Println("\n2. Device DNS Configuration:")
	fmt.Printf("   Set your device's DNS server to %s\n", hostIP)
	fmt.Println("\n3. Direct IP Access:")
	fmt.Printf("   Access projects directly via http://%s or https://%s\n", hostIP, hostIP)
	fmt.Println("\n4. Hosts File (Manual):")
	fmt.Println("   Add entries to your hosts file for each project:")
	fmt.Printf("   %s    project-name.test\n", hostIP)

	return nil
}

// handleDNSCleanup cleans up DNS configuration
func (h *Handler) handleDNSCleanup() error {
	fmt.Println("🧹 Cleaning up DNS configuration...")
	if err := utils.CleanupDNS(); err != nil {
		return fmt.Errorf("failed to cleanup DNS: %w", err)
	}
	fmt.Println("✅ DNS configuration cleaned up")
	return nil
}

// handleGitHubAuth handles GitHub authentication
func (h *Handler) handleGitHubAuth() error {
	return h.github.Authenticate()
}

// handleDatabaseAdd adds a database service
func (h *Handler) handleDatabaseAdd() error {
	// Get a name for the database instance
	fmt.Print("Enter a name for this database instance: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("no name provided")
	}
	name := strings.TrimSpace(scanner.Text())
	if name == "" {
		return fmt.Errorf("database name cannot be empty")
	}

	// Validate name (alphanumeric and underscore only)
	if !regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(name) {
		return fmt.Errorf("database name must be alphanumeric with underscores only")
	}

	// Show proper capitalization in UI
	options := []string{"MySQL", "PostgreSQL"}
	selected := utils.SelectOption("Select database type", options)

	// Map to internal names
	dbType := "mysql"
	if selected == "PostgreSQL" {
		dbType = "postgresql"
	}

	versions := []string{"8.0", "5.7"}
	if dbType == "postgresql" {
		versions = []string{"16", "15", "14", "13"}
	}

	version := utils.SelectOption(fmt.Sprintf("Select %s version", selected), versions)

	fmt.Printf("🚀 Adding %s '%s' (version %s)...\n", selected, name, version)

	if err := h.services.AddDatabase(name, dbType, version); err != nil {
		return fmt.Errorf("failed to add database: %w", err)
	}

	// Database port will be assigned automatically and shown by AddDatabase
	if dbType == "mysql" {
		fmt.Println("👤 User: root (no password)")
	} else {
		fmt.Println("👤 User: devbox, Password: devbox, Database: devbox")
	}

	return nil
}

// handleDatabaseRemove removes a database service
func (h *Handler) handleDatabaseRemove() error {
	databases, err := h.services.GetDatabases()
	if err != nil {
		return fmt.Errorf("failed to get databases: %w", err)
	}

	if len(databases) == 0 {
		fmt.Println("No databases configured")
		return nil
	}

	// Create list with database info
	dbOptions := make([]string, len(databases))
	for i, db := range databases {
		// Show name, type, and status
		dbType := "MySQL"
		if strings.Contains(db.Image, "postgres") {
			dbType = "PostgreSQL"
		}
		status := "stopped"
		if db.Status == "running" {
			status = "running"
		}
		dbOptions[i] = fmt.Sprintf("%s (%s - %s)", db.Name, dbType, status)
	}

	selected := utils.SelectOption("Select database to remove", dbOptions)

	// Extract the name from the selection
	selectedName := strings.Split(selected, " (")[0]

	if !utils.PromptYesNo(fmt.Sprintf("Remove database '%s'?", selectedName)) {
		fmt.Println("Remove cancelled")
		return nil
	}

	fmt.Printf("🗑️  Removing '%s'...\n", selectedName)

	if err := h.services.RemoveDatabase(selectedName); err != nil {
		return fmt.Errorf("failed to remove database: %w", err)
	}

	fmt.Printf("✅ Database '%s' removed\n", selectedName)
	return nil
}

// handleDatabaseList lists database services
func (h *Handler) handleDatabaseList() error {
	databases, err := h.services.GetDatabases()
	if err != nil {
		return fmt.Errorf("failed to get databases: %w", err)
	}

	if len(databases) == 0 {
		fmt.Println("No databases configured")
		fmt.Println("\nUse 'devbox database add' to add a database")
		return nil
	}

	fmt.Printf("Found %d database(s):\n\n", len(databases))
	for _, db := range databases {
		status := "🔴 stopped"
		if db.Status == "running" {
			status = "✅ running"
		}

		// Determine database type from image
		dbType := "MySQL"
		if strings.Contains(db.Image, "postgres") {
			dbType = "PostgreSQL"
		}

		fmt.Printf("  %s (%s) - %s\n", db.Name, dbType, status)
		fmt.Printf("    Image: %s\n", db.Image)
		if len(db.Ports) > 0 {
			fmt.Printf("    Port: %s\n", strings.Join(db.Ports, ", "))
		}
		fmt.Println()
	}

	return nil
}

// Helper methods for displaying information

func (h *Handler) showVersion() error {
	fmt.Printf("Devbox v%s\n", config.Version)
	return nil
}

func (h *Handler) showHelp() error {
	ShowHelp()
	return nil
}

func (h *Handler) showStatus() error {
	fmt.Printf("Devbox v%s - System Status\n", config.Version)
	fmt.Println(strings.Repeat("=", 50))

	// Installation status
	fmt.Printf("\n📦 Installation:\n")
	fmt.Printf("  Binary: %s %s\n", config.DevboxBinary, utils.BoolStatus(h.config.IsInstalled()))
	fmt.Printf("  Config: %s %s\n", h.config.ConfigFile, utils.BoolStatus(utils.FileExists(h.config.ConfigFile)))

	// Global services status
	fmt.Printf("\n🌐 Global Services:\n")
	svcs, _ := h.services.Status()
	for _, svc := range svcs {
		statusIcon := "🔴"
		if svc.Status == "running" {
			statusIcon = "✅"
		}
		fmt.Printf("  %s %s: %s\n", statusIcon, svc.Name, svc.Status)
	}

	// Current project status
	project, err := h.projects.GetCurrentProject()
	if err == nil && project.Status != "not initialized" {
		fmt.Printf("\n📁 Current Project:\n")
		fmt.Printf("  Name: %s\n", project.Name)
		fmt.Printf("  PHP: %s\n", project.PHPVersion)
		fmt.Printf("  Status: %s\n", utils.FormatStatus(project.Status))
		if project.Status == "running" {
			fmt.Printf("  URL: %s\n", project.URL)
		}
	}

	// Network information
	if hostIP, err := services.GetHostIP(); err == nil {
		fmt.Printf("\n🔗 Network:\n")
		fmt.Printf("  Host IP: %s\n", hostIP)
		fmt.Printf("  DNS Port: 5354\n")
	}

	fmt.Println()
	return nil
}

func (h *Handler) showProjectStatus(project *project.Project) error {
	status, err := h.projects.GetStatus()
	if err != nil {
		return err
	}

	fmt.Printf("Project: %s\n", status.Name)
	fmt.Printf("PHP Version: %s\n", status.PHPVersion)
	fmt.Printf("Status: %s\n", utils.FormatStatus(status.State))

	if status.State == "running" {
		fmt.Printf("URL: %s\n", status.URL)
		if status.Uptime != "" {
			fmt.Printf("Uptime: %s\n", status.Uptime)
		}
	}

	if len(status.Databases) > 0 {
		fmt.Printf("Databases: %s\n", strings.Join(status.Databases, ", "))
	}

	return nil
}

func (h *Handler) showServicesStatus() error {
	services, err := h.services.Status()
	if err != nil {
		return err
	}

	fmt.Println("\nGlobal Services Status:")
	fmt.Println(strings.Repeat("-", 50))

	for _, svc := range services {
		statusIcon := "🔴"
		if svc.Status == "running" {
			statusIcon = "✅"
		}

		fmt.Printf("%s %s\n", statusIcon, svc.Name)
		fmt.Printf("   Container: %s\n", svc.Container)
		fmt.Printf("   Status: %s\n", svc.Status)
		if len(svc.Ports) > 0 {
			fmt.Printf("   Ports: %s\n", strings.Join(svc.Ports, ", "))
		}
	}

	return nil
}

// ShowHelp displays the help message
func ShowHelp() {
	fmt.Printf(`Devbox v%s - Development Environment Manager

USAGE:
  devbox [command] [options]

COMMANDS:
  Project Management:
    start [version]    Start project (with optional PHP version)
    stop               Stop the current project
    restart            Restart the current project
    remove             Remove the current project
    status             Show project status
    list               List all projects
    php [version]      Change PHP version
    server-root [path] Change server root path

  Container Commands:
    exec <cmd>         Execute command in project container
    shell              Open interactive shell in container
    logs               Show container logs

  Global Services:
    global start       Start global services (DNS, Nginx, Mail)
    global stop        Stop global services
    global restart     Restart global services
    global status      Show services status

  Database:
    database add       Add a database service
    database remove    Remove a database service
    database list      List database services

  SSL Certificates:
    cert install       Install the CA certificate
    cert verify        Verify certificate installation
    cert debug         Debug certificate issues
    cert remove        Remove SSL certificate (fallback to HTTP)

  System:
    install            Install Devbox
    update             Update to latest version
    check-update       Check for available updates
    uninstall          Uninstall Devbox

EXAMPLES:
  devbox start              # Start project with default/saved PHP version
  devbox start 8.2          # Start project with PHP 8.2
  devbox server-root public # Change server root to 'public' directory
  devbox check-update       # Check for available updates
  devbox update             # Update to latest version
  devbox exec composer install  # Run composer install in container
  devbox exec npm run dev   # Run npm dev script in container
  devbox shell              # Open bash shell in container
  devbox logs              # View container logs
    version            Show version information
    help               Show this help message

  Network:
    network-help       Show network configuration help
    dns-cleanup        Clean up DNS configuration

  Authentication:
    github-auth        Setup GitHub authentication

  Other:
    logs [container]   Show container logs

QUICK START:
  1. Navigate to your project directory
  2. Run 'devbox init' to initialize
  3. Run 'devbox start' to launch
  4. Access at https://[project-name].test

EXAMPLES:
  devbox init 8.3           # Initialize with PHP 8.3
  devbox start              # Start current project
  devbox global start       # Start global services
  devbox database add       # Add a database
  devbox php 8.2           # Change to PHP 8.2

For more information, visit: https://devbox.app
`, config.Version)
}
