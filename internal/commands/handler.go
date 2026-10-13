package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/docker"
	"github.com/aliasproject/alios-cli/internal/github"
	"github.com/aliasproject/alios-cli/internal/project"
	"github.com/aliasproject/alios-cli/internal/services"
	"github.com/aliasproject/alios-cli/internal/share"
	"github.com/aliasproject/alios-cli/internal/ssl"
	"github.com/aliasproject/alios-cli/internal/updater"
	"github.com/aliasproject/alios-cli/internal/utils"
)

// Handler manages command execution
type Handler struct {
	docker   *docker.Client
	config   *config.Config
	projects *project.Manager
	services *services.Manager
	ssl      *ssl.Manager
	share    *share.Manager
	github   *github.Client
	updater  *updater.Updater
}

// NewHandler creates a new command handler
func NewHandler(dockerClient *docker.Client, cfg *config.Config) *Handler {
	sslManager := ssl.NewManager(dockerClient, cfg)
	return &Handler{
		docker:   dockerClient,
		config:   cfg,
		projects: project.NewManager(dockerClient),
		services: services.NewManager(dockerClient, sslManager),
		ssl:      sslManager,
		share:    share.NewManager(dockerClient, cfg, sslManager),
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
	case "rebuild":
		return h.handleRebuild()
	case "pause":
		return h.handlePause()
	case "resume":
		return h.handleResume()
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
	case "image":
		return h.handleImage(commandArgs)
	case "env":
		return h.handleEnv(commandArgs)
	case "share":
		return h.handleShare(commandArgs)

	// Container interaction
	case "exec", "run":
		return h.handleExec(commandArgs)
	case "shell", "sh", "bash":
		return h.handleShell(commandArgs)

	// Network and DNS
	case "network-help":
		return h.handleNetworkHelp()
	case "dns-cleanup":
		return h.handleDNSCleanup()
	case "dns-setup":
		return h.handleDNSSetup()

	// GitHub authentication
	case "github-auth", "auth":
		return h.handleGitHubAuth()

	case "github-verify":
		return h.handleGitHubVerify()

	case "doctor":
		return h.handleDoctor()

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

	fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " No alios project found in this directory")
	fmt.Println("Run 'alios start' to set up a project here, or 'alios help' for more information")
	return nil
}

// handleGlobalCommand handles global service commands
func (h *Handler) handleGlobalCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing global subcommand. Use: global [start|stop|restart|status]")
	}

	switch args[0] {
	case "start":
		fmt.Println(utils.Colorize("[STARTING]", utils.ColorCyan) + " Starting global services...")
		if err := h.services.Start(); err != nil {
			return fmt.Errorf("failed to start services: %w", err)
		}
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Global services started successfully")
		return h.showServicesStatus()

	case "stop":
		fmt.Println(utils.Colorize("[STOPPING]", utils.ColorYellow) + " Stopping global services...")
		if err := h.services.Stop(); err != nil {
			return fmt.Errorf("failed to stop services: %w", err)
		}
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Global services stopped")
		return nil

	case "restart":
		fmt.Println(utils.Colorize("[RESTARTING]", utils.ColorCyan) + " Restarting global services...")
		if err := h.services.Restart(); err != nil {
			return fmt.Errorf("failed to restart services: %w", err)
		}
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Global services restarted")
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

	case "repair":
		return h.handleCertRepair()

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

		fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" SSL certificate removed. Project now accessible via HTTP at: http://%s.test\n", proj.Name)
		return nil

	default:
		return fmt.Errorf("unknown cert command: %s", args[0])
	}
}

// handleCertRepair regenerates the Caddy vhost (and certificate, if missing) for
// every known project - running or stopped - without starting any project
// containers, then restarts Caddy once to pick up the changes. Use this after a
// vhost template change, or to recover Caddy from a crash loop caused by a stale
// vhost referencing a container that no longer exists.
func (h *Handler) handleCertRepair() error {
	projects, err := h.projects.List()
	if err != nil {
		return fmt.Errorf("failed to list projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " No projects found")
		return nil
	}

	names := make([]string, len(projects))
	for i, p := range projects {
		names[i] = p.Name
	}

	fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan)+" Repairing SSL/vhost configuration for %d project(s)...\n", len(names))

	repaired, repairErr := h.ssl.RepairVhosts(names)
	if repairErr != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: %v\n", repairErr)
	}

	fmt.Println(utils.Colorize("[RESTARTING]", utils.ColorCyan) + " Restarting Caddy to apply changes...")
	if err := h.ssl.RestartCaddy(); err != nil {
		return fmt.Errorf("failed to restart Caddy: %w", err)
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Repaired %d/%d vhost(s), Caddy restarted\n", repaired, len(names))
	return nil
}

// handleInstall installs Alios
func (h *Handler) handleInstall() error {
	if h.isFullySetUp() {
		fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Alios is already installed (v%s)\n", config.Version)
		return h.showStatus()
	}

	installer := NewInstaller(h.docker, h.config, h.services, h.ssl, h.github)
	return installer.Install()
}

// isFullySetUp reports whether installation actually finished - the binary
// is in place, global services are running, the CA certificate exists, and
// that CA is trusted on this host - rather than just checking
// config.IsInstalled(), which only means the binary got copied (step 4 of
// Install's ~8 steps). A failure anywhere after that point (e.g. a registry
// pull denied while starting global services, or the user declining the CA
// trust prompt) left IsInstalled() true with setup still incomplete, and
// handleInstall used to treat that as "done" and refuse to do anything
// further - notably, CAExists() alone can't tell a generated-and-trusted CA
// apart from one that's generated but still untrusted, so a declined/
// interrupted trust step used to be silently masked forever. Every step
// Install runs is itself safe to repeat (compose up, the CA-exists check,
// the binary copy, the trust-install prompt are all idempotent), so falling
// through to installer.Install() here resumes from wherever a previous
// attempt stopped instead of starting over.
func (h *Handler) isFullySetUp() bool {
	return h.config.IsInstalled() && h.services.IsRunning() && h.ssl.CAExists() && h.ssl.IsCertificateInstalled()
}

// ensureUpdaterAuthenticated makes sure the updater has a GitHub token before
// checking/downloading a release. Releases are published to GitHub directly
// and this is a private repo, so there's no other channel to fall back to if
// authentication isn't available -- the caller's subsequent CheckForUpdate/
// Update call will simply fail with a clear GitHub API error in that case.
func (h *Handler) ensureUpdaterAuthenticated() {
	if token := h.config.GetGithubToken(); token != "" {
		h.updater.SetGitHubToken(token)
		return
	}

	fmt.Println("🔍 Checking for updates...")
	if err := h.github.EnsureAuthenticated(); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: No GitHub authentication available.\n")
		fmt.Printf("   Run 'alios github-auth' to authenticate for access to releases.\n\n")
		return
	}

	if token := h.config.GetGithubToken(); token != "" {
		h.updater.SetGitHubToken(token)
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " GitHub authentication found, checking for updates...")
	}
}

// handleUpdate updates Alios to the latest version
func (h *Handler) handleUpdate() error {
	h.ensureUpdaterAuthenticated()

	return h.updater.Update(false)
}

// handleCheckUpdate checks for available updates without installing
func (h *Handler) handleCheckUpdate() error {
	h.ensureUpdaterAuthenticated()

	// Check for updates
	release, isNewer, err := h.updater.CheckForUpdate()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !isNewer {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " You are running the latest version")
		return nil
	}

	fmt.Printf("New version available: v%s\n", release.Version)
	if release.Critical {
		fmt.Println(utils.Colorize("[WARNING]", utils.ColorYellow) + " This is a critical security update")
	}
	if release.Notes != "" {
		fmt.Printf("\nRelease notes:\n%s\n", release.Notes)
	}
	fmt.Printf("\nRun 'alios update' to install the latest version.\n")

	return nil
}

// handleUninstall uninstalls Alios
func (h *Handler) handleUninstall() error {
	if !utils.PromptYesNo("Are you sure you want to uninstall Alios?") {
		fmt.Println("Uninstall cancelled")
		return nil
	}

	fmt.Println("🗑 Uninstalling Alios...")

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
	os.Remove(config.AliosBinary)

	// No local configuration directory to remove since we use environment variables only
	fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Configuration is stored in environment variables and project directories only")

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Alios uninstalled")
	return nil
}

// handleInit initializes a new project
func (h *Handler) handleInit(args []string) error {
	// Init is now just an alias for start with PHP version selection
	fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " 'alios init' is deprecated. Using 'alios start' instead...")
	return h.handleStart(args...)
}

// handleStart starts the current project
func (h *Handler) handleStart(args ...string) error {
	// Get project info early for cleanup purposes
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	// Select the image to run first - this has to come before the PHP
	// version prompt below. PHP version only means anything for the
	// prebuilt path (a custom Dockerfile bakes in its own base image, and a
	// custom pull is whatever the pulled image already is), so asking for
	// it before knowing which path was chosen means asking a question
	// that's irrelevant for two of the three choices. Only prompted on
	// first start. Mirrors production Alios's prebuilt/custom-Dockerfile/
	// custom-pull choice (see project.ImageChoice).
	var imageChoice project.ImageChoice
	if proj.Status == "not initialized" {
		useDockerfile := false
		if project.HasDockerfile(proj.Path) {
			choice := utils.SelectOption("A Dockerfile was found in this project. How should it be used?", []string{
				"Use this project's Dockerfile",
				"Use a prebuilt Alios image",
				"Pull a different image",
			})
			switch choice {
			case "Use this project's Dockerfile":
				imageChoice.ImageType = "custom"
				imageChoice.ImageSource = "build"
				imageChoice.DockerfilePath = "Dockerfile"
				imageChoice.BuildContext = "."
				imageChoice.BuildArgs = promptForBuildArgs(filepath.Join(proj.Path, "Dockerfile"))
				useDockerfile = true
			case "Pull a different image":
				imageChoice.ImageType = "custom"
				imageChoice.ImageSource = "pull"
				imageChoice.ImageURL = utils.GetInput("Image to pull (e.g. myuser/myimage:tag): ")
			}
		}

		if !useDockerfile && imageChoice.ImageType != "custom" {
			if framework := project.DetectFramework(proj.Path); framework != "" {
				fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" Detected a %s project - using the %s-optimized image\n", framework, framework)
				imageChoice.Framework = framework
			}
		}
	}

	// Select PHP version if not specified - only meaningful for the
	// prebuilt path (a custom Dockerfile or pulled image brings its own PHP
	// version; this CLI doesn't choose it for either), so skip the prompt
	// entirely when imageChoice picked one of those.
	phpVersion := h.config.DefaultPHPVersion
	if len(args) > 0 && project.ValidatePHPVersion(args[0]) {
		phpVersion = args[0]
	} else if proj.Status == "not initialized" && imageChoice.ImageType != "custom" {
		// Only prompt for PHP version on first start
		phpVersion = utils.SelectOption("Select PHP Version", project.GetAvailablePHPVersions())
	}

	// Collect server root path (default to "/app")
	serverRoot := "/app/public"
	if proj.Status == "not initialized" {
		// Only prompt for server root on first start
		input := utils.GetInput("Enter server root path (default: /app/public): ")
		if input != "" {
			serverRoot = input
		}
	}

	// Setup cleanup function that will be called on any failure
	cleanupOnFailure := func(reason string) {
		fmt.Printf(utils.Colorize("[CLEANUP]", utils.ColorCyan)+" Cleaning up after failure: %s\n", reason)
		containerName := fmt.Sprintf("alios_php_%s", proj.Name)

		// Try to stop the container first
		if stopErr := h.docker.Run("stop", containerName); stopErr != nil {
			// Don't log if container doesn't exist
			if !strings.Contains(stopErr.Error(), "No such container") {
				fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to stop container: %v\n", stopErr)
			}
		}

		// Force remove the container
		if removeErr := h.docker.Run("rm", "-f", containerName); removeErr != nil {
			// Don't log if container doesn't exist
			if !strings.Contains(removeErr.Error(), "No such container") {
				fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to remove container: %v\n", removeErr)
			}
		}
	}

	// Ensure global services are running
	if err := h.services.EnsureRunning(); err != nil {
		return fmt.Errorf("failed to start global services: %w", err)
	}

	// Ensure GitHub authentication
	if err := h.github.EnsureAuthenticated(); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: GitHub authentication failed: %v\n", err)
	}

	fmt.Printf(utils.Colorize("[STARTING]", utils.ColorCyan)+" Starting project '%s' with PHP %s...\n", proj.Name, phpVersion)

	if err := h.projects.Start(phpVersion, imageChoice, serverRoot); err != nil {
		cleanupOnFailure("project start failed")
		return fmt.Errorf("failed to start project: %w", err)
	}

	// Setup SSL for project
	if h.config.EnableSSL {
		if err := h.ssl.SetupProjectSSL(proj.Name); err != nil {
			// SSL setup failed - cleanup and return error
			fmt.Printf(utils.Colorize("[ERROR]", utils.ColorRed)+" Failed to setup SSL certificate: %v\n", err)
			cleanupOnFailure("SSL certificate setup failed")

			return fmt.Errorf("project startup failed due to SSL certificate error. Please check:\n"+
				"  1. Caddy container is running (alios global status)\n"+
				"  2. CA certificate exists (alios cert verify)\n"+
				"  3. Try running: alios global restart\n"+
				"Error details: %w", err)
		}

		// The certificate just generated is only valid in the browser if the
		// CA that signed it is trusted on this host - that's a separate,
		// one-time step (`alios cert install`) that can be skipped or
		// interrupted independently of SSL setup succeeding here. Surface it
		// now rather than let the user discover it as a silent "Not Secure"
		// warning in Chrome.
		if !h.ssl.IsCertificateInstalled() {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " The CA certificate isn't trusted on this machine yet - your browser will show this site as untrusted.\n")
			fmt.Println("   Run 'alios cert install' to trust it, then restart your browser.")
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
		fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Project started successfully with PHP %s\n", phpVersion)
		fmt.Printf(utils.Colorize("[WEB]", utils.ColorBlue)+" Access your project at: %s\n", project.URL)
		return h.showProjectStatus(project)
	}

	// Something went wrong if we get here
	cleanupOnFailure("project started but status is unclear")
	return fmt.Errorf("project started but status is unclear. Run 'alios status' to check")
}

// promptForBuildArgs asks for a value for each ARG a Dockerfile declares
// with no default, rather than silently feeding it something like the
// current GitHub auth token - a build-arg's value (e.g. a credential for a
// private package registry) isn't necessarily something the user wants used
// for an arbitrary local build without being asked first. Returns nil if the
// Dockerfile declares no such ARGs.
func promptForBuildArgs(dockerfilePath string) map[string]string {
	argNames := project.DetectBuildArgs(dockerfilePath)
	if len(argNames) == 0 {
		return nil
	}

	fmt.Println("This Dockerfile declares build arguments with no default value:")
	buildArgs := make(map[string]string)
	for _, name := range argNames {
		value := utils.GetInput(fmt.Sprintf("  %s (leave blank to skip): ", name))
		if value != "" {
			buildArgs[name] = value
		}
	}
	return buildArgs
}

// handleImage switches the current project's image mode (prebuilt, build
// from its Dockerfile, or pull a specific image) without needing to remove
// and recreate the project from scratch - the same switch handleStart offers
// on first init, available again any time after.
func (h *Handler) handleImage(args []string) error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'alios start' first")
	}

	mode := ""
	if len(args) > 0 {
		mode = args[0]
	} else {
		mode = utils.SelectOption("Switch to which image mode?", []string{"prebuilt", "dockerfile", "pull"})
	}

	var imageChoice project.ImageChoice
	switch mode {
	case "prebuilt":
		imageChoice.ImageType = "prebuilt"
		if framework := project.DetectFramework(proj.Path); framework != "" {
			fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" Detected a %s project - using the %s-optimized image\n", framework, framework)
			imageChoice.Framework = framework
		}

	case "dockerfile":
		if !project.HasDockerfile(proj.Path) {
			return fmt.Errorf("no Dockerfile found in %s", proj.Path)
		}
		imageChoice.ImageType = "custom"
		imageChoice.ImageSource = "build"
		imageChoice.DockerfilePath = "Dockerfile"
		imageChoice.BuildContext = "."
		imageChoice.BuildArgs = promptForBuildArgs(filepath.Join(proj.Path, "Dockerfile"))

	case "pull":
		imageURL := ""
		if len(args) > 1 {
			imageURL = args[1]
		} else {
			imageURL = utils.GetInput("Image to pull (e.g. myuser/myimage:tag): ")
		}
		if imageURL == "" {
			return fmt.Errorf("no image specified")
		}
		imageChoice.ImageType = "custom"
		imageChoice.ImageSource = "pull"
		imageChoice.ImageURL = imageURL

	default:
		return fmt.Errorf("unknown image mode '%s'. Use: prebuilt, dockerfile, or pull", mode)
	}

	fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan)+" Switching '%s' to %s...\n", proj.Name, mode)
	if err := h.projects.ChangeImage(imageChoice); err != nil {
		return fmt.Errorf("failed to switch image: %w", err)
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Image switched successfully")
	updated, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}
	return h.showProjectStatus(updated)
}

// handleStop stops the current project
func (h *Handler) handleStop() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	fmt.Printf(utils.Colorize("[STOPPING]", utils.ColorYellow)+" Stopping project '%s'...\n", project.Name)

	if err := h.projects.Stop(); err != nil {
		return fmt.Errorf("failed to stop project: %w", err)
	}

	h.stopShareQuietly(project.Name)

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Project stopped")
	return nil
}

// handleRestart restarts the current project
func (h *Handler) handleRestart() error {
	if err := h.handleStop(); err != nil {
		return err
	}
	return h.handleStart()
}

// handleRebuild forces the current project's container to be recreated from
// a freshly resolved image. Start already detects a stale prebuilt/pulled
// image on its own; this is for when you've just edited a custom Dockerfile
// (image=build) and want that picked up immediately rather than only on the
// next PHP-version change.
func (h *Handler) handleRebuild() error {
	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if err := h.projects.Rebuild(); err != nil {
		return fmt.Errorf("failed to rebuild project: %w", err)
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" '%s' rebuilt and running\n", project.Name)
	return nil
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
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to clean up SSL certificates: %v\n", err)
	}

	fmt.Println(utils.Colorize("[WEB]", utils.ColorBlue) + " Removing vhost configuration...")
	if err := h.ssl.RemoveProjectVhost(project.Name); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to remove vhost configuration: %v\n", err)
	}

	h.stopShareQuietly(project.Name)

	// Remove project
	if err := h.projects.Remove(true); err != nil {
		return fmt.Errorf("failed to remove project: %w", err)
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Project removed")
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
		status := utils.Colorize("[STOPPED]", utils.ColorRed) + " stopped"
		if proj.Status == "running" {
			status = utils.Colorize("[RUNNING]", utils.ColorGreen) + " running"
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
		return fmt.Errorf("project not initialized. Run 'alios start' first")
	}

	// PHP version only means anything on the prebuilt path - it's just the
	// tag on ghcr.io/aliasproject/php. A custom Dockerfile or pulled image
	// brings its own PHP version baked in, so changing this label wouldn't
	// actually change anything running; better to say so plainly than let
	// someone pick a version and wonder why nothing happened.
	if proj.ImageType == "custom" {
		fmt.Printf(utils.Colorize("[INFO]", utils.ColorBlue)+" '%s' is running a custom image (%s), not the prebuilt PHP image - PHP version isn't something alios-cli controls here.\n", proj.Name, proj.ImageSource)
		fmt.Println("Run 'alios image prebuilt' first if you want to switch back and manage the PHP version through alios-cli.")
		return nil
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
		fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Already using PHP %s\n", newVersion)
		return nil
	}

	fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan)+" Changing PHP version to %s...\n", newVersion)

	if err := h.projects.ChangePHPVersion(newVersion); err != nil {
		return fmt.Errorf("failed to change PHP version: %w", err)
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" PHP version changed to %s\n", newVersion)
	fmt.Println("Run 'alios start' to start with the new version")
	return nil
}

// handleServerRoot changes server root for the current project
func (h *Handler) handleServerRoot(args []string) error {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'alios start' first")
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
			fmt.Println(utils.Colorize("[ERROR]", utils.ColorRed) + " Server root cannot be empty")
			return nil
		}
		newServerRoot = input
	}

	if newServerRoot == currentServerRoot {
		fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Already using server root: %s\n", newServerRoot)
		return nil
	}

	fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan)+" Changing server root to %s...\n", newServerRoot)

	if err := h.projects.ChangeServerRoot(newServerRoot); err != nil {
		return fmt.Errorf("failed to change server root: %w", err)
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Server root changed to %s\n", newServerRoot)
	if proj.Status == "running" {
		fmt.Println(utils.Colorize("[RESTARTING]", utils.ColorCyan) + " Restarting container to apply changes...")
		if err := h.handleRestart(); err != nil {
			return fmt.Errorf("failed to restart container: %w", err)
		}
	} else {
		fmt.Println("Run 'alios start' to start with the new server root")
	}
	return nil
}

// handleEnv manages the env vars injected into the project container:
//
//	alios env list
//	alios env set KEY=VALUE [KEY2=VALUE2 ...]
//	alios env unset KEY [KEY2 ...]
//
// Values persist in the container's alios.project.env label; set/unset
// recreate the container so the change takes effect immediately.
func (h *Handler) handleEnv(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing env subcommand. Use: env list | env set KEY=VALUE ... | env unset KEY ...")
	}

	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if proj.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'alios start' first")
	}

	switch args[0] {
	case "list", "ls":
		if len(proj.Env) == 0 {
			fmt.Println("No env vars set. Use 'alios env set KEY=VALUE' to add one")
			return nil
		}
		for _, key := range project.SortedEnvKeys(proj.Env) {
			fmt.Printf("%s=%s\n", key, proj.Env[key])
		}
		return nil

	case "set":
		if len(args) < 2 {
			return fmt.Errorf("missing env vars. Use: env set KEY=VALUE [KEY2=VALUE2 ...]")
		}
		vars := make(map[string]string, len(args)-1)
		for _, arg := range args[1:] {
			key, value, err := project.ParseEnvPair(arg)
			if err != nil {
				return err
			}
			vars[key] = value
		}

		if err := h.projects.SetEnv(vars); err != nil {
			return fmt.Errorf("failed to set env vars: %w", err)
		}

		for _, key := range project.SortedEnvKeys(vars) {
			fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Set %s\n", key)
		}
		h.printEnvApplied(proj)
		return nil

	case "unset", "rm", "remove":
		if len(args) < 2 {
			return fmt.Errorf("missing keys. Use: env unset KEY [KEY2 ...]")
		}
		keys := args[1:]
		var missing []string
		for _, key := range keys {
			if _, ok := proj.Env[key]; !ok {
				missing = append(missing, key)
			}
		}
		for _, key := range missing {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" %s is not set\n", key)
		}
		if len(missing) == len(keys) {
			return nil
		}

		if err := h.projects.UnsetEnv(keys); err != nil {
			return fmt.Errorf("failed to unset env vars: %w", err)
		}

		for _, key := range keys {
			if _, ok := proj.Env[key]; ok {
				fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Unset %s\n", key)
			}
		}
		h.printEnvApplied(proj)
		return nil

	default:
		return fmt.Errorf("unknown env subcommand %q. Use: env list | env set KEY=VALUE ... | env unset KEY ...", args[0])
	}
}

// printEnvApplied tells the user what SetEnv/UnsetEnv just did to the
// container, based on the state it was in before the change.
func (h *Handler) printEnvApplied(proj *project.Project) {
	if proj.Status == "running" {
		fmt.Println(utils.Colorize("[RESTARTING]", utils.ColorCyan) + " Container recreated to apply the change")
	} else {
		fmt.Println("Run 'alios start' to start with the new env vars")
	}
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
		return fmt.Errorf("project container is not running. Run 'alios start' first")
	}

	// Prepare command for execution
	execCommand := append([]string{command}, args...)
	err = h.projects.Exec(execCommand, true)
	if err != nil {
		// If the error indicates the command wasn't found, provide a helpful message
		if strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("'%s' is not an alios command or available in the container\nTry 'alios help' for available commands or 'alios shell' to explore the container", command)
		}
	}
	return err
}

// handleExec explicitly executes a command in the project container
func (h *Handler) handleExec(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command specified. Usage: alios exec <command> [args...]")
	}

	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'alios start' first")
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running. Run 'alios start' first")
	}

	err = h.projects.Exec(args, false)
	if err != nil {
		// The project manager already provides a good error message
		return err
	}
	return nil
}

// handleShell opens an interactive shell in the project container
func (h *Handler) handleShell(args []string) error {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	userFlag := fs.String("user", "", "Run the shell as a specific user (name or uid:gid). Defaults to your host user.")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("shell: %w", err)
	}

	project, err := h.projects.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized. Run 'alios start' first")
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running. Run 'alios start' first")
	}

	if *userFlag != "" {
		fmt.Printf("📂 Opening shell in %s container as user '%s'...\n", project.Name, *userFlag)
	} else {
		fmt.Printf("📂 Opening shell in %s container...\n", project.Name)
	}

	return h.projects.OpenShell(*userFlag)
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
	if !strings.HasPrefix(container, "alios_") {
		container = "alios_" + container
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
	fmt.Println(utils.Colorize("[CLEANUP]", utils.ColorCyan) + " Cleaning up DNS configuration...")
	if err := utils.CleanupDNS(); err != nil {
		return fmt.Errorf("failed to cleanup DNS: %w", err)
	}
	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " DNS configuration cleaned up")
	return nil
}

// handleDNSSetup sets up DNS resolver configuration for .test domains
func (h *Handler) handleDNSSetup() error {
	fmt.Println(utils.Colorize("[NETWORK]", utils.ColorCyan) + " Setting up DNS resolver for .test domains...")
	if err := utils.SetupTestDomainResolver(); err != nil {
		return fmt.Errorf("failed to setup DNS resolver: %w", err)
	}
	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " DNS resolver configured. .test domains will now resolve correctly.")
	return nil
}

// handleGitHubAuth handles GitHub authentication
func (h *Handler) handleGitHubAuth() error {
	return h.github.Authenticate()
}

// handleGitHubVerify verifies GitHub authentication and GHCR access
func (h *Handler) handleGitHubVerify() error {
	fmt.Println("🔍 Verifying GitHub Authentication")
	fmt.Println(strings.Repeat("-", 50))

	// Check if token exists
	token := h.config.GetGithubToken()
	if token == "" {
		fmt.Println(utils.Colorize("[ERROR]", utils.ColorRed) + " No GitHub token found")
		fmt.Println("\nRun 'alios github-auth' to authenticate")
		return fmt.Errorf("not authenticated")
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " GitHub token found in configuration")

	// Verify we can pull from ghcr.io
	fmt.Println("\n[AUTH] Testing access to ghcr.io...")

	// Try to pull a small test image
	testImage := "ghcr.io/aliasproject/dns:latest"
	fmt.Printf("Attempting to pull: %s\n", testImage)

	if err := h.docker.PullImage(testImage, nil); err != nil {
		fmt.Printf(utils.Colorize("[ERROR]", utils.ColorRed)+" Failed to pull image: %v\n", err)
		fmt.Println("\n[INFO] Troubleshooting:")
		fmt.Println("   1. Verify Docker login: docker login ghcr.io")
		fmt.Println("   2. Check token permissions: needs 'read:packages' scope")
		fmt.Println("   3. Try re-authenticating: alios github-auth")
		return fmt.Errorf("ghcr.io access failed")
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Successfully pulled image from ghcr.io")
	fmt.Println("\n[COMPLETE] GitHub authentication is working correctly!")

	return nil
}

// handleDatabaseAdd adds a database service
func (h *Handler) handleDatabaseAdd() error {
	// Show proper capitalization in UI
	options := []string{"MySQL", "PostgreSQL"}
	selected := utils.SelectOption("Select database type", options)

	// Map to internal names and use as database name
	var dbType, name string
	if selected == "PostgreSQL" {
		dbType = "postgresql"
		name = "postgresql"
	} else {
		dbType = "mysql"
		name = "mysql"
	}

	versions := []string{"8.0", "5.7"}
	if dbType == "postgresql" {
		versions = []string{"16", "15", "14", "13"}
	}

	version := utils.SelectOption(fmt.Sprintf("Select %s version", selected), versions)

	fmt.Printf(utils.Colorize("[STARTING]", utils.ColorCyan)+" Adding %s '%s' (version %s)...\n", selected, name, version)

	if err := h.services.AddDatabase(name, dbType, version); err != nil {
		return fmt.Errorf("failed to add database: %w", err)
	}

	// Database port will be assigned automatically and shown by AddDatabase
	if dbType == "mysql" {
		fmt.Println(utils.Colorize("[USER]", utils.ColorCyan) + " User: root (no password)")
	} else {
		fmt.Println(utils.Colorize("[USER]", utils.ColorCyan) + " User: alios, Password: alios, Database: alios")
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

	fmt.Printf(utils.Colorize("[REMOVING]", utils.ColorYellow)+" Removing '%s'...\n", selectedName)

	if err := h.services.RemoveDatabase(selectedName); err != nil {
		return fmt.Errorf("failed to remove database: %w", err)
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" Database '%s' removed\n", selectedName)
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
		fmt.Println("\nUse 'alios database add' to add a database")
		return nil
	}

	fmt.Printf("Found %d database(s):\n\n", len(databases))
	for _, db := range databases {
		status := utils.Colorize("[STOPPED]", utils.ColorRed) + " stopped"
		if db.Status == "running" {
			status = utils.Colorize("[RUNNING]", utils.ColorGreen) + " running"
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
	fmt.Printf("Alios v%s\n", config.Version)
	return nil
}

func (h *Handler) showHelp() error {
	ShowHelp()
	return nil
}

func (h *Handler) showStatus() error {
	fmt.Printf("Alios v%s - System Status\n", config.Version)
	fmt.Println(strings.Repeat("=", 50))

	// Installation status
	fmt.Printf("\n" + utils.Colorize("[PACKAGE]", utils.ColorCyan) + " Installation:\n")
	fmt.Printf("  Binary: %s %s\n", config.AliosBinary, utils.BoolStatus(h.config.IsInstalled()))
	fmt.Printf("  Config: Environment variables %s\n", utils.BoolStatus(h.config.GetGithubToken() != ""))

	// Global services status
	fmt.Printf("\n" + utils.Colorize("[WEB]", utils.ColorBlue) + " Global Services:\n")
	svcs, _ := h.services.Status()
	for _, svc := range svcs {
		statusIcon := utils.Colorize("[STOPPED]", utils.ColorRed)
		if svc.Status == "running" {
			statusIcon = utils.Colorize("[RUNNING]", utils.ColorGreen)
		}
		fmt.Printf("  %s %s: %s\n", statusIcon, svc.Name, svc.Status)
	}

	// Current project status
	project, err := h.projects.GetCurrentProject()
	if err == nil && project.Status != "not initialized" {
		fmt.Printf("\n" + utils.Colorize("[DIRECTORY]", utils.ColorCyan) + " Current Project:\n")
		fmt.Printf("  Name: %s\n", project.Name)
		fmt.Printf("  PHP: %s\n", project.PHPVersion)
		fmt.Printf("  Status: %s\n", utils.FormatStatus(project.Status))
		if project.Status == "running" {
			fmt.Printf("  URL: %s\n", project.URL)
		}
		if sh, err := h.share.Get(project.Name); err == nil && sh != nil {
			fmt.Printf("  Shared: %s\n", formatShare(sh, h.share.TunnelRunning(sh)))
		}
	}

	// Network information
	if hostIP, err := services.GetHostIP(); err == nil {
		fmt.Printf("\n" + utils.Colorize("[NETWORK]", utils.ColorCyan) + " Network:\n")
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
		statusIcon := utils.Colorize("[STOPPED]", utils.ColorRed)
		if svc.Status == "running" {
			statusIcon = utils.Colorize("[RUNNING]", utils.ColorGreen)
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
	fmt.Printf(`Alios v%s - Development Environment Manager

USAGE:
  alios [command] [options]

COMMANDS:
  Project Management:
    start [version]    Start project (with optional PHP version)
    stop               Stop the current project
    restart            Restart the current project
    rebuild            Force-recreate the container from a fresh image
                        (e.g. after editing a custom Dockerfile)
    remove             Remove the current project
    status             Show project status
    list               List all projects
    php [version]      Change PHP version
    server-root [path] Change server root path
    image [mode]       Switch image mode: prebuilt, dockerfile, or pull
    env list           Show env vars injected into the container
    env set K=V ...    Set one or more env vars (recreates the container)
    env unset K ...    Remove one or more env vars (recreates the container)
    share [--auth u:p] Share the project at a public URL (Cloudflare Tunnel)
    share stop         Stop sharing the current project
    share list         List every active share

  Machine Lifecycle:
    pause              Stop every alios container (projects + global services)
    resume             Restart exactly what was paused

  Container Commands:
    exec <cmd>         Execute command in project container
    shell [--user=<u>] Open interactive shell in container
    logs               Show container logs

  Global Services:
    global start       Start global services (DNS, Caddy, Mail)
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
    cert repair        Regenerate all vhosts (no container start) + restart Caddy

  System:
    doctor             Check Docker, GitHub/GHCR access, DNS, services, SSL,
                        and the current project, with a fix for each failure
    install            Install Alios
    update             Update to latest version
    check-update       Check for available updates
    uninstall          Uninstall Alios

EXAMPLES:
  alios start              # Start project with default/saved PHP version
  alios start 8.2          # Start project with PHP 8.2
  alios server-root public # Change server root to 'public' directory
  alios image prebuilt     # Switch back to the prebuilt image
  alios image dockerfile   # Switch to building from the project's Dockerfile
  alios env set APP_DEBUG=true DB_HOST=alios_mysql  # Inject env vars
  alios env unset APP_DEBUG # Remove an env var
  alios share              # Get a public URL for the current project
  alios share --auth demo:secret  # Same, behind a username/password
  alios doctor             # Diagnose why something isn't working
  alios check-update       # Check for available updates
  alios update             # Update to latest version
  alios pause              # Stop everything, free up all CPU/RAM
  alios resume             # Bring back exactly what was paused
  alios exec composer install  # Run composer install in container
  alios exec npm run dev   # Run npm dev script in container
  alios shell              # Open shell as your host user
  alios shell --user=root  # Open shell as root
  alios logs              # View container logs
    version            Show version information
    help               Show this help message

  Network:
    network-help       Show network configuration help
    dns-setup          Set up DNS resolver for .test domains
    dns-cleanup        Clean up DNS configuration

  Authentication:
    github-auth        Setup GitHub authentication
    github-verify      Verify GitHub authentication and GHCR access

  Other:
    logs [container]   Show container logs

QUICK START:
  1. Navigate to your project directory
  2. Run 'alios start' to set it up and launch it (first run asks for
     PHP version, server root, and image)
  3. Access at https://[project-name].test

EXAMPLES:
  alios start 8.4          # First start with PHP 8.4
  alios start              # Start current project
  alios global start       # Start global services
  alios database add       # Add a database
  alios php 8.2           # Change to PHP 8.2

For more information, visit: https://alios.app
`, config.Version)
}
