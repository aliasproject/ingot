package project

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/docker"
	"github.com/aliasproject/alios-cli/internal/utils"
)

// Manager handles project operations
type Manager struct {
	docker *docker.Client
	config *config.Config
	mu     sync.RWMutex
}

// NewManager creates a new project manager
func NewManager(dockerClient *docker.Client) *Manager {
	return &Manager{
		docker: dockerClient,
		config: config.Get(),
	}
}

// Project represents a Alios project
type Project struct {
	Name       string
	Path       string
	PHPVersion string
	ServerRoot string
	Status     string
	Container  string
	URL        string
	CreatedAt  time.Time
	UpdatedAt  time.Time

	// Image selection - mirrors production Alios's container_config vocabulary
	// (image_type/custom_image_source/etc, see app/Actions/Site/CreateApplication.php
	// in the Alios platform repo), expressed as container labels instead of a
	// database row since this CLI has no persisted config of its own.
	ImageType      string            // "prebuilt" (default) or "custom"
	ImageSource    string            // "pull" or "build", only meaningful when ImageType == "custom"
	ImageURL       string            // only meaningful when ImageSource == "pull"
	DockerfilePath string            // only meaningful when ImageSource == "build"; default "Dockerfile"
	BuildContext   string            // only meaningful when ImageSource == "build"; default "."
	BuildArgs      map[string]string // only meaningful when ImageSource == "build"; values for the Dockerfile's no-default ARGs
	Framework      string            // "" or "wordpress"; detected from the project directory
}

// ImageChoice carries a project's first-init image and framework selection
// through Start into createContainer. Every field is optional: an empty field
// leaves whatever is already stored on the project (read from its container
// labels, or the defaults GetCurrentProject applies for a brand-new project)
// untouched. Callers only need to populate this when proj.Status ==
// "not initialized" - handleStart's prompts run once, the same way the
// PHP-version and server-root prompts already do.
type ImageChoice struct {
	ImageType      string
	ImageSource    string
	ImageURL       string
	DockerfilePath string
	BuildContext   string
	BuildArgs      map[string]string
	Framework      string
}

// GetCurrentProject returns the current project based on working directory
func (m *Manager) GetCurrentProject() (*Project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}

	name := filepath.Base(cwd)
	container := m.getContainerName(name)

	// Check if project is initialized
	info, err := m.docker.InspectContainer(container)
	if err != nil {
		if err == docker.ErrContainerNotFound {
			return &Project{
				Name:           name,
				Path:           cwd,
				ServerRoot:     "public", // default for new projects
				Status:         "not initialized",
				Container:      container,
				ImageType:      "prebuilt",
				DockerfilePath: "Dockerfile",
				BuildContext:   ".",
			}, nil
		}
		return nil, err
	}

	// Get configuration from Docker labels
	phpVersion := info.Labels["alios.project.php_version"]
	if phpVersion == "" {
		phpVersion = config.DefaultPHPVersion
	}

	serverRoot := info.Labels["alios.project.server_root"]
	if serverRoot == "" {
		serverRoot = "public" // default
	}

	imageType := info.Labels["alios.project.image_type"]
	if imageType == "" {
		imageType = "prebuilt"
	}

	dockerfilePath := info.Labels["alios.project.dockerfile_path"]
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}

	buildContext := info.Labels["alios.project.build_context"]
	if buildContext == "" {
		buildContext = "."
	}

	buildArgs := parseBuildArgs(info.Labels["alios.project.build_args"])

	status := "stopped"
	if info.State == "running" {
		status = "running"
	}

	return &Project{
		Name:           name,
		Path:           cwd,
		PHPVersion:     phpVersion,
		ServerRoot:     serverRoot,
		Status:         status,
		Container:      container,
		URL:            fmt.Sprintf("https://%s.test", name),
		CreatedAt:      info.CreatedAt,
		UpdatedAt:      info.StartedAt,
		ImageType:      imageType,
		ImageSource:    info.Labels["alios.project.image_source"],
		ImageURL:       info.Labels["alios.project.image_url"],
		DockerfilePath: dockerfilePath,
		BuildContext:   buildContext,
		BuildArgs:      buildArgs,
		Framework:      info.Labels["alios.project.framework"],
	}, nil
}

// serializeBuildArgs/parseBuildArgs round-trip build-arg values through a
// single Docker label as comma-separated KEY=VALUE pairs, the same way every
// other piece of project state here lives in labels instead of a config
// file. Values containing a comma aren't supported by this simple encoding.
func serializeBuildArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(args))
	for key, value := range args {
		pairs = append(pairs, key+"="+value)
	}
	return strings.Join(pairs, ",")
}

func parseBuildArgs(encoded string) map[string]string {
	if encoded == "" {
		return nil
	}
	args := make(map[string]string)
	for _, pair := range strings.Split(encoded, ",") {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		args[key] = value
	}
	return args
}

// Initialize creates a new project in the current directory
func (m *Manager) Initialize(phpVersion string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status != "not initialized" {
		return fmt.Errorf("project already initialized")
	}

	// No project configuration to save - everything is stored in Docker labels

	return nil
}

// Start starts the project container with optional PHP version, server root,
// and (first-init only) image/framework choice. See ImageChoice's doc comment
// - pass ImageChoice{} when the project is already initialized.
func (m *Manager) Start(phpVersion string, imageChoice ImageChoice, serverRoot ...string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	// Determine PHP version to use
	var desiredPHPVersion string
	if phpVersion != "" {
		desiredPHPVersion = phpVersion
	} else {
		// Use current project's version or default
		desiredPHPVersion = project.PHPVersion
		if desiredPHPVersion == "" {
			desiredPHPVersion = m.config.DefaultPHPVersion
		}
	}

	// Determine server root to use
	var desiredServerRoot string
	if len(serverRoot) > 0 && serverRoot[0] != "" {
		desiredServerRoot = serverRoot[0]
	} else {
		// Use current project's server root or default
		desiredServerRoot = project.ServerRoot
		if desiredServerRoot == "" {
			desiredServerRoot = "public" // default
		}
	}

	// Apply any first-init image/framework choice on top of what's already
	// stored (or defaulted) on the project. Empty fields leave the existing
	// value alone, so this is a no-op for an already-initialized project.
	if imageChoice.ImageType != "" {
		project.ImageType = imageChoice.ImageType
	}
	if imageChoice.ImageSource != "" {
		project.ImageSource = imageChoice.ImageSource
	}
	if imageChoice.ImageURL != "" {
		project.ImageURL = imageChoice.ImageURL
	}
	if imageChoice.DockerfilePath != "" {
		project.DockerfilePath = imageChoice.DockerfilePath
	}
	if imageChoice.BuildContext != "" {
		project.BuildContext = imageChoice.BuildContext
	}
	if len(imageChoice.BuildArgs) > 0 {
		project.BuildArgs = imageChoice.BuildArgs
	}
	if imageChoice.Framework != "" {
		project.Framework = imageChoice.Framework
	}

	// Check if container exists
	if m.docker.ContainerExists(project.Container) {
		// Check if the existing container's PHP version or injected .env
		// content is still current. Recreating on an .env change mirrors
		// production: RunAndSwapContainer always recreates the container on
		// any config change too, never patches env vars into a running one
		// (Docker has no way to do that regardless).
		info, err := m.docker.InspectContainer(project.Container)
		if err == nil {
			existingVersion := info.Labels["alios.project.php_version"]
			versionChanged := existingVersion != "" && existingVersion != desiredPHPVersion

			existingEnvHash := info.Labels["alios.project.env_hash"]
			envChanged := existingEnvHash != EnvFileHash(project.Path)

			if versionChanged || envChanged {
				reason := "PHP version changed"
				if envChanged && !versionChanged {
					reason = ".env changed"
				} else if envChanged && versionChanged {
					reason = "PHP version and .env changed"
				}
				fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan)+" %s, recreating container...\n", reason)
				if err := m.docker.RemoveContainer(project.Container, true); err != nil {
					return fmt.Errorf("failed to remove old container: %w", err)
				}
				project.PHPVersion = desiredPHPVersion
				if err := m.createContainer(project); err != nil {
					return fmt.Errorf("failed to recreate container: %w", err)
				}
				return nil
			}
		}

		// Start existing container
		if err := m.docker.StartContainer(project.Container); err != nil {
			return fmt.Errorf("failed to start container: %w", err)
		}
	} else {
		// Create new container
		project.PHPVersion = desiredPHPVersion
		project.ServerRoot = desiredServerRoot
		if err := m.createContainer(project); err != nil {
			// Clean up any partially created container on failure
			cleanupErr := m.docker.RemoveContainer(project.Container, true)
			if cleanupErr != nil {
				// Log but don't fail on cleanup error
				fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to cleanup container after creation failure: %v\n", cleanupErr)
			}
			return fmt.Errorf("failed to create container: %w", err)
		}
	}

	return nil
}

// Stop stops the project container
func (m *Manager) Stop() error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	if err := m.docker.StopContainer(project.Container, 10); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	return nil
}

// Remove removes the project container and configuration
func (m *Manager) Remove(force bool) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	// Stop and remove container
	if err := m.docker.RemoveContainer(project.Container, force); err != nil {
		if !force {
			return fmt.Errorf("failed to remove container: %w", err)
		}
		// When force is true, log the error but continue with cleanup
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to remove container normally: %v\n", err)

		// Try one more time with direct docker command
		if forcedErr := m.docker.Run("rm", "-f", project.Container); forcedErr != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Forced removal also failed: %v\n", forcedErr)
		}
	}

	// No project configuration files to remove

	return nil
}

// ChangePHPVersion changes the PHP version for the project
func (m *Manager) ChangePHPVersion(newVersion string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	if project.PHPVersion == newVersion {
		return nil // Already using this version
	}

	// Stop and remove existing container
	if err := m.docker.RemoveContainer(project.Container, true); err != nil {
		return fmt.Errorf("failed to remove old container: %w", err)
	}

	// Create new container with new PHP version
	project.PHPVersion = newVersion
	if err := m.createContainer(project); err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}

	return nil
}

// ChangeImage switches the current project to a different image mode
// (prebuilt, build from its Dockerfile, or pull a specific image),
// destroying and recreating its container the same way ChangePHPVersion
// does. Unlike the ImageChoice merge Start() does during first-init (which
// only overrides fields the caller explicitly set, leaving the rest as
// already stored), this replaces the image configuration wholesale - the
// caller is responsible for building a complete target state, including
// zeroing fields that don't apply to the new mode (e.g. a Dockerfile path
// left over from a previous custom build shouldn't linger once switched
// back to prebuilt). The project's domain/SSL/vhost are untouched: the
// container name doesn't change, only the image backing it.
func (m *Manager) ChangeImage(choice ImageChoice) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	project.ImageType = choice.ImageType
	project.ImageSource = choice.ImageSource
	project.ImageURL = choice.ImageURL
	project.DockerfilePath = choice.DockerfilePath
	project.BuildContext = choice.BuildContext
	project.BuildArgs = choice.BuildArgs
	project.Framework = choice.Framework

	if err := m.docker.RemoveContainer(project.Container, true); err != nil {
		return fmt.Errorf("failed to remove old container: %w", err)
	}

	if err := m.createContainer(project); err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}

	return nil
}

// ChangeServerRoot changes the server root for the current project
func (m *Manager) ChangeServerRoot(newServerRoot string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	// Get current server root from config
	currentServerRoot := project.ServerRoot
	if currentServerRoot == "" {
		currentServerRoot = "public" // default
	}

	if currentServerRoot == newServerRoot {
		return nil // Already using this server root
	}

	// Stop and remove existing container if running
	if project.Status == "running" {
		if err := m.docker.RemoveContainer(project.Container, true); err != nil {
			return fmt.Errorf("failed to remove old container: %w", err)
		}
	}

	// Create new container with new server root if it was running
	if project.Status == "running" {
		project.ServerRoot = newServerRoot
		if err := m.createContainer(project); err != nil {
			return fmt.Errorf("failed to create new container: %w", err)
		}
	}

	return nil
}

// List returns all Alios projects
func (m *Manager) List() ([]*Project, error) {
	containers, err := m.docker.ListContainers("label=alios.project.name")
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	projects := make([]*Project, 0, len(containers))

	for _, containerName := range containers {
		if containerName == "" {
			continue
		}

		info, err := m.docker.InspectContainer(containerName)
		if err != nil {
			continue
		}

		name := info.Labels["alios.project.name"]
		if name == "" {
			name = strings.TrimPrefix(containerName, "alios_php_")
		}

		phpVersion := info.Labels["alios.project.php_version"]
		if phpVersion == "" {
			phpVersion = config.DefaultPHPVersion
		}

		status := "stopped"
		if info.State == "running" {
			status = "running"
		}

		projects = append(projects, &Project{
			Name:       name,
			Path:       info.Labels["alios.project.path"],
			PHPVersion: phpVersion,
			Status:     status,
			Container:  containerName,
			URL:        fmt.Sprintf("https://%s.test", name),
			CreatedAt:  info.CreatedAt,
			UpdatedAt:  info.StartedAt,
		})
	}

	return projects, nil
}

// ListRunning returns all Alios project containers that are currently running,
// system-wide (not just the one for the current working directory).
func (m *Manager) ListRunning() ([]*Project, error) {
	projects, err := m.List()
	if err != nil {
		return nil, err
	}

	running := make([]*Project, 0, len(projects))
	for _, p := range projects {
		if p.Status == "running" {
			running = append(running, p)
		}
	}

	return running, nil
}

// StopAll stops every currently-running project container on the machine, not just
// the one for the current directory. It is best-effort: a failure to stop one
// container is logged as a warning and does not prevent stopping the rest. It
// returns the container names that were successfully stopped.
func (m *Manager) StopAll() ([]string, error) {
	projects, err := m.ListRunning()
	if err != nil {
		return nil, fmt.Errorf("failed to list running projects: %w", err)
	}

	stopped := make([]string, 0, len(projects))
	for _, p := range projects {
		if err := m.docker.StopContainer(p.Container, 10); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to stop project '%s': %v\n", p.Name, err)
			continue
		}
		stopped = append(stopped, p.Container)
	}

	return stopped, nil
}

// StartNamed starts exactly the given project container names (used when resuming
// a previous pause). Containers that no longer exist are skipped with a warning
// rather than treated as a hard failure. It returns the container names that were
// successfully started.
func (m *Manager) StartNamed(containerNames []string) ([]string, error) {
	started := make([]string, 0, len(containerNames))
	for _, name := range containerNames {
		if err := m.docker.StartContainer(name); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to start project container '%s': %v\n", name, err)
			continue
		}
		started = append(started, name)
	}

	return started, nil
}

// Exec executes a command in the project container
func (m *Manager) Exec(command []string, interactive bool) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running")
	}

	err = m.docker.ExecCommand(project.Container, interactive, hostUser(), command...)
	if err != nil {
		// Check for common error patterns and provide better messages
		errStr := err.Error()
		if strings.Contains(errStr, "executable file not found in $PATH") {
			cmdName := ""
			if len(command) > 0 {
				cmdName = command[0]
			}
			return fmt.Errorf("command '%s' not found in container. Try 'alios shell' to explore available commands", cmdName)
		}
		if strings.Contains(errStr, "OCI runtime exec failed") {
			// Extract just the useful part of the error
			if strings.Contains(errStr, "exec failed:") {
				parts := strings.Split(errStr, "exec failed:")
				if len(parts) > 1 {
					return fmt.Errorf("container exec failed:%s", parts[1])
				}
			}
		}
		// Return original error if no specific case matched
		return err
	}
	return nil
}

// OpenShell opens an interactive shell in the project container.
// userOverride specifies which user to run the shell as (e.g. "root", "1000:1000").
// Pass "" to default to the host user so that file ownership stays consistent
// with the mounted project files.
func (m *Manager) OpenShell(userOverride string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running")
	}

	// Use the caller-supplied user when provided; otherwise fall back to the
	// host user so that files touched in the shell keep the same owner as
	// the mounted project files.
	u := userOverride
	if u == "" {
		u = hostUser()
	}

	return m.docker.ExecCommand(project.Container, true, u, "sh")
}

// GetStatus returns detailed status of the current project
func (m *Manager) GetStatus() (*ProjectStatus, error) {
	project, err := m.GetCurrentProject()
	if err != nil {
		return nil, err
	}

	status := &ProjectStatus{
		Name:       project.Name,
		Path:       project.Path,
		PHPVersion: project.PHPVersion,
		Container:  project.Container,
		URL:        project.URL,
		State:      project.Status,
	}

	// Get additional info if container exists
	if project.Status != "not initialized" {
		info, err := m.docker.InspectContainer(project.Container)
		if err == nil {
			status.Networks = info.Networks
			status.Labels = info.Labels
			status.CreatedAt = info.CreatedAt.Format(time.RFC3339)
			if info.State == "running" {
				status.StartedAt = info.StartedAt.Format(time.RFC3339)
				status.Uptime = time.Since(info.StartedAt).Round(time.Second).String()
			}
		}
	}

	// Auto-detect running databases instead of loading from config
	databases, err := m.getRunningDatabases()
	if err == nil {
		status.Databases = databases
	}
	// No environment or ports config - everything is ephemeral

	return status, nil
}

// ProjectStatus holds detailed project status information
type ProjectStatus struct {
	Name        string            `json:"name"`
	Path        string            `json:"path"`
	PHPVersion  string            `json:"php_version"`
	Container   string            `json:"container"`
	URL         string            `json:"url"`
	State       string            `json:"state"`
	Networks    []string          `json:"networks,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Databases   []string          `json:"databases,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Ports       map[string]string `json:"ports,omitempty"`
	CreatedAt   string            `json:"created_at,omitempty"`
	StartedAt   string            `json:"started_at,omitempty"`
	Uptime      string            `json:"uptime,omitempty"`
}

// createContainer creates a new container for the project
func (m *Manager) createContainer(project *Project) error {
	networkConfig := config.GetNetworkConfig()

	// Ensure network exists
	if err := m.docker.CreateNetwork(networkConfig.BridgeName); err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	// Resolve which image to run. Mirrors production Alios's image_type/
	// custom_image_source decision tree (ResolvePrebuiltImage.php,
	// CreateApplication.php) - see the Project/ImageChoice doc comments.
	var image string
	switch project.ImageType {
	case "custom":
		switch project.ImageSource {
		case "pull":
			image = project.ImageURL
			if err := m.docker.PullImage(image, nil); err != nil {
				// Continue even if pull fails - image might exist locally
				fmt.Printf("Warning: Failed to pull image: %v\n", err)
			}
		case "build":
			// Local build tag, mirroring production's local/{site}:latest
			// convention. No PullImage call at all: the Dockerfile itself
			// encodes whatever the image needs, same as production skipping
			// registry auth entirely for a local build.
			image = fmt.Sprintf("local/%s:latest", project.Name)
			if err := m.docker.BuildImage(&docker.BuildConfig{
				Tag:            image,
				DockerfilePath: filepath.Join(project.Path, project.DockerfilePath),
				Context:        filepath.Join(project.Path, project.BuildContext),
				BuildArgs:      project.BuildArgs,
			}); err != nil {
				return fmt.Errorf("failed to build image: %w", err)
			}
		default:
			return fmt.Errorf("project has image_type=custom but no image_source set")
		}
	default: // "prebuilt"
		tag := fmt.Sprintf("%s-frankenphp-dev", project.PHPVersion)
		if project.Framework == "wordpress" {
			tag = fmt.Sprintf("%s-frankenphp-wordpress-dev", project.PHPVersion)
		}
		image = fmt.Sprintf("ghcr.io/aliasproject/php:%s", tag)
		if err := m.docker.PullImage(image, nil); err != nil {
			// Continue even if pull fails - image might exist locally
			fmt.Printf("Warning: Failed to pull image: %v\n", err)
		}
	}

	// Resolve the DNS container's live IP on the internal network rather than
	// relying on a hardcoded address that may belong to a different container.
	dnsServers := []string{"1.1.1.1", "8.8.8.8"} // fallback to public resolvers
	if dnsIP, err := m.docker.GetContainerIP("alios_dns", networkConfig.BridgeName); err == nil && dnsIP != "" {
		dnsServers = []string{dnsIP, "1.1.1.1", "8.8.8.8"}
	} else {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Could not resolve alios_dns IP (%v); falling back to public DNS. .test domains will not resolve.\n", err)
	}

	// Load the project's own .env, if it has one, and inject it the same way
	// production supplies a site's config: plain KEY=VALUE env vars on the
	// container (see RunDockerContainer's buildEnvironmentVariables in the
	// Alios platform repo), just sourced from the local file instead of a
	// database-backed setting. Set after copying in .env so these always win
	// regardless of what .env contains - mirrors production setting
	// SERVER_ROOT last, specifically so a stray env var can never silently
	// override it. PORT matters beyond the prebuilt image's own defaults: a
	// custom Dockerfile's baked Caddyfile can reference {$PORT} with no
	// fallback of its own (production always supplies it - see
	// buildDockerCommand's `-e PORT=...` - so its own Caddyfiles are never
	// written to expect a default), and an empty substitution there is a
	// hard Caddyfile parse failure, not a graceful fallback.
	envVars, err := LoadEnvFile(project.Path)
	if err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to read .env: %v\n", err)
		envVars = nil
	}
	if envVars == nil {
		envVars = make(map[string]string)
	}
	envVars["SERVER_NAME"] = ":80"
	envVars["SERVER_ROOT"] = project.ServerRoot
	envVars["HTTPS"] = "on"
	envVars["PORT"] = "80"

	// Create container configuration
	containerConfig := &docker.ContainerConfig{
		Name:     project.Container,
		Image:    image,
		Detached: true,
		Restart:  "unless-stopped",
		Network:  networkConfig.BridgeName,
		Volumes: []string{
			fmt.Sprintf("%s:/app", project.Path),
		},
		Environment: envVars,
		Labels: map[string]string{
			"alios.project.name":            project.Name,
			"alios.project.path":            project.Path,
			"alios.project.php_version":     project.PHPVersion,
			"alios.project.server_root":     project.ServerRoot,
			"alios.project.image_type":      project.ImageType,
			"alios.project.image_source":    project.ImageSource,
			"alios.project.image_url":       project.ImageURL,
			"alios.project.dockerfile_path": project.DockerfilePath,
			"alios.project.build_context":   project.BuildContext,
			"alios.project.build_args":      serializeBuildArgs(project.BuildArgs),
			"alios.project.framework":       project.Framework,
			"alios.project.env_hash":        EnvFileHash(project.Path),
		},
		DNS: dnsServers,
	}

	// No additional configuration needed - everything is ephemeral or in Docker labels

	if err := m.docker.RunContainer(containerConfig); err != nil {
		// Best effort cleanup on failure - remove any partially created container
		cleanupErr := m.docker.RemoveContainer(project.Container, true)
		if cleanupErr != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to cleanup container after run failure: %v\n", cleanupErr)
		}
		return fmt.Errorf("failed to run container: %w", err)
	}

	return nil
}

// hostUser returns the current host user as "uid:gid" so that docker exec
// sessions run with the same ownership as the mounted project files.
// Falls back to an empty string (container default / root) if detection fails.
func hostUser() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s:%s", u.Uid, u.Gid)
}

// getContainerName returns the container name for a project
func (m *Manager) getContainerName(projectName string) string {
	return fmt.Sprintf("alios_php_%s", projectName)
}

// ValidatePHPVersion checks if a PHP version is valid
func ValidatePHPVersion(version string) bool {
	validVersions := []string{"8.5", "8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}
	for _, v := range validVersions {
		if v == version {
			return true
		}
	}
	return false
}

// GetAvailablePHPVersions returns available PHP versions
func GetAvailablePHPVersions() []string {
	return []string{"8.5", "8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}
}

// getRunningDatabases returns a list of currently running database services
func (m *Manager) getRunningDatabases() ([]string, error) {
	containers, err := m.docker.ListContainers("label=alios.service.type=database")
	if err != nil {
		return nil, err
	}

	var databases []string
	for _, container := range containers {
		// Extract database name from container name (alios_mysql -> mysql)
		if strings.HasPrefix(container, "alios_") && !strings.Contains(container, "_caddy") && !strings.Contains(container, "_mailhog") {
			dbName := strings.TrimPrefix(container, "alios_")
			databases = append(databases, dbName)
		}
	}

	return databases, nil
}
