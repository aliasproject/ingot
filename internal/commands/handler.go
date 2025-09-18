package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
	"github.com/aliasproject/devbox/internal/github"
	"github.com/aliasproject/devbox/internal/project"
	"github.com/aliasproject/devbox/internal/services"
	"github.com/aliasproject/devbox/internal/ssl"
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
	case "uninstall":
		return h.handleUninstall()

	// Project management
	case "init":
		return h.handleInit(commandArgs)
	case "start":
		return h.handleStart()
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
		return fmt.Errorf("missing cert subcommand. Use: cert [install|verify|debug]")
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
	updater := NewUpdater(h.config, h.github)
	return updater.Update()
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
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if proj.Status != "not initialized" {
		fmt.Printf("✅ Project '%s' is already initialized with PHP %s\n", proj.Name, proj.PHPVersion)
		return h.showProjectStatus(proj)
	}

	// Select PHP version
	phpVersion := h.config.DefaultPHPVersion
	if len(args) > 0 && project.ValidatePHPVersion(args[0]) {
		phpVersion = args[0]
	} else {
		phpVersion = utils.SelectOption("Select PHP Version", project.GetAvailablePHPVersions())
	}

	fmt.Printf("🚀 Initializing project '%s' with PHP %s...\n", proj.Name, phpVersion)

	if err := h.projects.Initialize(phpVersion); err != nil {
		return fmt.Errorf("failed to initialize project: %w", err)
	}

	fmt.Printf("✅ Project initialized successfully\n")
	fmt.Printf("Run 'devbox start' to start the project\n")
	return nil
}

// handleStart starts the current project
func (h *Handler) handleStart() error {
	// Ensure global services are running
	if err := h.services.EnsureRunning(); err != nil {
		return fmt.Errorf("failed to start global services: %w", err)
	}

	// Ensure GitHub authentication
	if err := h.github.EnsureAuthenticated(); err != nil {
		fmt.Printf("⚠️  Warning: GitHub authentication failed: %v\n", err)
	}

	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	fmt.Printf("🚀 Starting project '%s'...\n", project.Name)

	if err := h.projects.Start(); err != nil {
		return fmt.Errorf("failed to start project: %w", err)
	}

	// Setup SSL for project
	if h.config.EnableSSL {
		if err := h.ssl.SetupProjectSSL(project.Name); err != nil {
			fmt.Printf("⚠️  Warning: Failed to setup SSL: %v\n", err)
		}
	}

	project, _ = h.projects.GetCurrentProject()
	fmt.Printf("✅ Project started successfully\n")
	fmt.Printf("🌐 Access your project at: %s\n", project.URL)

	return h.showProjectStatus(project)
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

	// Remove SSL certificates
	if h.config.EnableSSL {
		h.ssl.RemoveProjectSSL(project.Name)
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

// handleContainerExec executes a command in the project container
func (h *Handler) handleContainerExec(command string, args []string) error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("unknown command: %s (project not initialized)", command)
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running")
	}

	// Prepare command for execution
	execCommand := append([]string{command}, args...)
	return h.projects.Exec(execCommand, true)
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
	dbType := utils.SelectOption("Select database type", []string{"mysql", "postgresql"})

	versions := []string{"8.0", "5.7"}
	if dbType == "postgresql" {
		versions = []string{"16", "15", "14", "13"}
	}

	version := utils.SelectOption(fmt.Sprintf("Select %s version", dbType), versions)

	fmt.Printf("🚀 Adding %s %s...\n", dbType, version)

	if err := h.services.AddDatabase(dbType, version); err != nil {
		return fmt.Errorf("failed to add database: %w", err)
	}

	port := "3306"
	if dbType == "postgresql" {
		port = "5432"
	}

	fmt.Printf("✅ %s %s added successfully\n", dbType, version)
	fmt.Printf("📍 Available at localhost:%s\n", port)

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
		fmt.Println("No databases running")
		return nil
	}

	dbNames := make([]string, len(databases))
	for i, db := range databases {
		dbNames[i] = db.Name
	}

	dbType := dbNames[0]
	if len(dbNames) > 1 {
		dbType = utils.SelectOption("Select database to remove", dbNames)
	}

	if !utils.PromptYesNo(fmt.Sprintf("Remove %s database?", dbType)) {
		fmt.Println("Remove cancelled")
		return nil
	}

	fmt.Printf("🗑 Removing %s...\n", dbType)

	if err := h.services.RemoveDatabase(dbType); err != nil {
		return fmt.Errorf("failed to remove database: %w", err)
	}

	fmt.Printf("✅ %s removed\n", dbType)
	return nil
}

// handleDatabaseList lists database services
func (h *Handler) handleDatabaseList() error {
	databases, err := h.services.GetDatabases()
	if err != nil {
		return fmt.Errorf("failed to get databases: %w", err)
	}

	if len(databases) == 0 {
		fmt.Println("No databases running")
		return nil
	}

	fmt.Printf("Found %d database(s):\n\n", len(databases))
	for _, db := range databases {
		status := "🔴 stopped"
		if db.Status == "running" {
			status = "✅ running"
		}

		fmt.Printf("  %s (%s)\n", db.Name, status)
		fmt.Printf("    Image: %s\n", db.Image)
		if len(db.Ports) > 0 {
			fmt.Printf("    Ports: %s\n", strings.Join(db.Ports, ", "))
		}
		fmt.Println()
	}

	return nil
}

// Helper methods for displaying information

func (h *Handler) showVersion() error {
	fmt.Printf("Devbox v%s\n", config.Version)
	fmt.Printf("GitHub: https://github.com/%s\n", config.GithubRepo)
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
    init [version]      Initialize a new project (optional PHP version)
    start              Start the current project
    stop               Stop the current project
    restart            Restart the current project
    remove             Remove the current project
    status             Show project status
    list               List all projects
    php [version]      Change PHP version

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

  System:
    install            Install Devbox
    update             Update to latest version
    uninstall          Uninstall Devbox
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

For more information, visit: https://github.com/%s
`, config.Version, config.GithubRepo)
}
