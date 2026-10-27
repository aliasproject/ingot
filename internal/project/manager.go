package project

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aliasproject/ingot/internal/config"
	"github.com/aliasproject/ingot/internal/docker"
	"github.com/aliasproject/ingot/internal/idle"
	"github.com/aliasproject/ingot/internal/utils"
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

// Project represents a Ingot project
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

	// Env holds user-supplied env vars for the container, managed with
	// `ingot env set/unset` and stored in the ingot.project.env label. This
	// is how a project gets config into its container the same way
	// production supplies it (plain KEY=VALUE vars on `docker run`, see
	// RunDockerContainer's buildEnvironmentVariables in the Alios platform
	// repo). Ingot deliberately does not read the project's own .env: the
	// project directory is bind-mounted, so an app that reads .env itself
	// (Laravel, etc.) already sees edits instantly, and injecting the same
	// keys as real env vars would only shadow the file with a stale copy
	// until the next restart.
	Env map[string]string

	// Idle is the project's scale-to-zero setting, from the
	// ingot.project.idle label (`ingot idle`): "" for the global
	// INGOT_IDLE_TIMEOUT, "off" to never stop it, or its own timeout.
	Idle string
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
	phpVersion := info.Labels["ingot.project.php_version"]
	if phpVersion == "" {
		phpVersion = config.DefaultPHPVersion
	}

	serverRoot := info.Labels["ingot.project.server_root"]
	if serverRoot == "" {
		serverRoot = "public" // default
	}

	imageType := info.Labels["ingot.project.image_type"]
	if imageType == "" {
		imageType = "prebuilt"
	}

	dockerfilePath := info.Labels["ingot.project.dockerfile_path"]
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}

	buildContext := info.Labels["ingot.project.build_context"]
	if buildContext == "" {
		buildContext = "."
	}

	buildArgs := parseBuildArgs(info.Labels["ingot.project.build_args"])
	env := parseEnv(info.Labels[envLabel])

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
		ImageSource:    info.Labels["ingot.project.image_source"],
		ImageURL:       info.Labels["ingot.project.image_url"],
		DockerfilePath: dockerfilePath,
		BuildContext:   buildContext,
		BuildArgs:      buildArgs,
		Framework:      info.Labels["ingot.project.framework"],
		Env:            env,
		Idle:           info.Labels[idle.IdleLabel],
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
		// Check if the existing container's PHP version is still current.
		// Env var changes never surface here: `ingot env set/unset` recreates
		// the container itself (Docker has no way to patch env vars into a
		// running one, and neither does production's RunAndSwapContainer).
		info, err := m.docker.InspectContainer(project.Container)
		if err == nil {
			existingVersion := info.Labels["ingot.project.php_version"]
			versionChanged := existingVersion != "" && existingVersion != desiredPHPVersion

			// Only worth a registry round-trip when nothing else already
			// forces a recreate -- resolveImage builds the prebuilt tag off
			// project.PHPVersion, so this must run with the version this
			// start actually committed to, not whatever was on disk before.
			stale := false
			if !versionChanged {
				project.PHPVersion = desiredPHPVersion
				stale = m.imageIsStale(project, info)
			}

			if versionChanged || stale {
				var reasons []string
				if versionChanged {
					reasons = append(reasons, "PHP version changed")
				}
				if stale {
					reasons = append(reasons, "a newer image is available")
				}
				fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan)+" %s, recreating container...\n", strings.Join(reasons, " and "))
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

// Rebuild forces the current project's container to be recreated from a
// freshly resolved image, regardless of whether Start would otherwise
// notice anything changed. Start's own staleness check (imageIsStale) only
// covers registry-backed images -- a local Dockerfile build has no upstream
// tag to compare against, so editing one and running "start" again would
// otherwise keep the old container running forever. This is the deliberate,
// on-demand escape hatch for that case (and works the same way for the
// other image types too).
func (m *Manager) Rebuild() error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status == "not initialized" {
		return fmt.Errorf("no project to rebuild")
	}

	if m.docker.ContainerExists(project.Container) {
		fmt.Printf(utils.Colorize("[REBUILDING]", utils.ColorCyan)+" Recreating '%s'...\n", project.Container)
		if err := m.docker.RemoveContainer(project.Container, true); err != nil {
			return fmt.Errorf("failed to remove existing container: %w", err)
		}
	}

	return m.createContainer(project)
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

// SetEnv adds or replaces user env vars on the current project and recreates
// the container so they take effect. Reserved infra keys are rejected. If the
// container was stopped it's left stopped after the recreate, so this only
// changes config, never whether the project is running.
func (m *Manager) SetEnv(vars map[string]string) error {
	for key := range vars {
		if IsReservedEnvKey(key) {
			return fmt.Errorf("%s is set by ingot itself and cannot be overridden", key)
		}
	}

	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}
	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	if project.Env == nil {
		project.Env = make(map[string]string)
	}
	changed := false
	for key, value := range vars {
		if existing, ok := project.Env[key]; !ok || existing != value {
			project.Env[key] = value
			changed = true
		}
	}
	if !changed {
		return nil
	}

	return m.recreate(project)
}

// UnsetEnv removes user env vars from the current project and recreates the
// container. Keys that aren't set are ignored.
func (m *Manager) UnsetEnv(keys []string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}
	if project.Status == "not initialized" {
		return fmt.Errorf("project not initialized")
	}

	changed := false
	for _, key := range keys {
		if _, ok := project.Env[key]; ok {
			delete(project.Env, key)
			changed = true
		}
	}
	if !changed {
		return nil
	}

	return m.recreate(project)
}

// recreate replaces the project's container with one carrying project's
// current settings (env vars, idle setting), preserving the running/stopped
// state it had before. The container has to be recreated even when stopped:
// its labels are the only place these settings persist, so removing it and
// waiting for the next start would lose them.
func (m *Manager) recreate(project *Project) error {
	wasRunning := project.Status == "running"

	if err := m.docker.RemoveContainer(project.Container, true); err != nil {
		return fmt.Errorf("failed to remove old container: %w", err)
	}
	if err := m.createContainer(project); err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
	}
	if !wasRunning {
		if err := m.docker.StopContainer(project.Container, 10); err != nil {
			return fmt.Errorf("failed to stop recreated container: %w", err)
		}
	}
	return nil
}

// List returns all Ingot projects
func (m *Manager) List() ([]*Project, error) {
	containers, err := m.docker.ListContainers("label=ingot.project.name")
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

		name := info.Labels["ingot.project.name"]
		if name == "" {
			name = strings.TrimPrefix(containerName, "ingot_php_")
		}

		phpVersion := info.Labels["ingot.project.php_version"]
		if phpVersion == "" {
			phpVersion = config.DefaultPHPVersion
		}

		status := "stopped"
		if info.State == "running" {
			status = "running"
		}

		projects = append(projects, &Project{
			Name:       name,
			Path:       info.Labels["ingot.project.path"],
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

// ListRunning returns all Ingot project containers that are currently running,
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
			return fmt.Errorf("command '%s' not found in container. Try 'ingot shell' to explore available commands", cmdName)
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

// resolveImage figures out which image a project's container should run,
// pulling or building it as needed, and returns the reference to run it
// with. Mirrors production Alios's image_type/custom_image_source decision
// tree (ResolvePrebuiltImage.php, CreateApplication.php) - see the
// Project/ImageChoice doc comments.
//
// Factored out of createContainer so Start can also call it to check an
// already-running container for staleness (see imageIsStale) before
// deciding whether starting it as-is is actually correct, and so Rebuild
// can force the same resolution on demand.
func (m *Manager) resolveImage(project *Project) (string, error) {
	switch project.ImageType {
	case "custom":
		switch project.ImageSource {
		case "pull":
			image := project.ImageURL
			if err := m.pullOrUseLocal(image); err != nil {
				return "", err
			}
			return image, nil
		case "build":
			// Local build tag, mirroring production's local/{site}:latest
			// convention. No PullImage call at all: the Dockerfile itself
			// encodes whatever the image needs, same as production skipping
			// registry auth entirely for a local build.
			image := fmt.Sprintf("local/%s:latest", project.Name)
			if err := m.docker.BuildImage(&docker.BuildConfig{
				Tag:            image,
				DockerfilePath: filepath.Join(project.Path, project.DockerfilePath),
				Context:        filepath.Join(project.Path, project.BuildContext),
				BuildArgs:      project.BuildArgs,
			}); err != nil {
				return "", fmt.Errorf("failed to build image: %w", err)
			}
			return image, nil
		default:
			return "", fmt.Errorf("project has image_type=custom but no image_source set")
		}
	default: // "prebuilt"
		image := m.ImageReference(project)
		if err := m.pullOrUseLocal(image); err != nil {
			return "", err
		}
		return image, nil
	}
}

// ImageReference returns the image a project's container runs, without
// pulling or building anything. This is the pure half of resolveImage, so
// `ingot doctor` can report (and probe registry access for) the exact tag
// a not-yet-initialized project would use.
func (m *Manager) ImageReference(project *Project) string {
	if project.ImageType == "custom" {
		if project.ImageSource == "build" {
			return fmt.Sprintf("local/%s:latest", project.Name)
		}
		return project.ImageURL
	}
	phpVersion := project.PHPVersion
	if phpVersion == "" {
		phpVersion = m.config.DefaultPHPVersion
	}
	tag := fmt.Sprintf("%s-frankenphp-dev", phpVersion)
	if project.Framework == "wordpress" {
		tag = fmt.Sprintf("%s-frankenphp-wordpress-dev", phpVersion)
	}
	return fmt.Sprintf("ghcr.io/aliasproject/php:%s", tag)
}

// pullOrUseLocal pulls a registry image, tolerating a failed pull only when
// a copy is already present locally (offline work, a flaky registry). If
// the pull fails *and* nothing is cached, it returns a real error instead
// of letting `docker run` fail later with an opaque exit status 125 -- and
// names the likely cause when the registry said "denied": ingot's images are
// public, so that's almost always a stale ghcr.io login in Docker's config
// (older ingot versions ran `docker login ghcr.io`) whose token has been
// revoked or expired - Docker still sends it, and GHCR refuses it.
func (m *Manager) pullOrUseLocal(image string) error {
	err := m.docker.PullImage(image, nil)
	if err == nil {
		return nil
	}
	if m.docker.ImageExists(image) {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Failed to pull %s (%v); using the locally cached copy\n", image, err)
		return nil
	}

	msg := fmt.Sprintf("image %s could not be pulled and is not available locally: %v", image, err)
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "denied") || strings.Contains(lower, "unauthorized") {
		msg += "\n  The registry refused access. Ingot's images are public, so this is usually a saved ghcr.io" +
			"\n  login in Docker's config whose token has been revoked or expired - Docker still sends it." +
			"\n  Fix: docker logout ghcr.io" +
			"\n  Then run 'ingot doctor' to confirm registry access."
	} else if strings.Contains(lower, "not found") || strings.Contains(lower, "manifest unknown") {
		msg += "\n  The registry has no such tag. Run 'ingot doctor' to see which image this project resolves to."
	} else {
		msg += "\n  Run 'ingot doctor' to check Docker and registry access."
	}
	return fmt.Errorf("%s", msg)
}

// imageIsStale reports whether a registry-backed image (prebuilt, or an
// explicitly pulled custom image) has moved on since the given container
// was created from it. Always returns false for a local Dockerfile build --
// there's no upstream tag for it to drift from; Rebuild is the deliberate,
// on-demand way to pick up a local Dockerfile edit instead.
//
// This exists because Start only ever pulled fresh at first container
// creation (see createContainer) -- a container left running for months
// would silently keep whatever image it was created from even after the
// tag it came from published security/hardening fixes. Same shape of bug
// as recordDeployedImageDigest() in the main Alios platform trusting a
// stale cache instead of checking fresh; same fix, checked here instead.
func (m *Manager) imageIsStale(project *Project, info *docker.ContainerInfo) bool {
	if project.ImageType == "custom" && project.ImageSource != "pull" {
		return false
	}

	image, err := m.resolveImage(project)
	if err != nil {
		return false
	}

	latestID, err := m.docker.GetImageID(image)
	if err != nil || latestID == "" {
		return false
	}

	return latestID != info.ImageID
}

// hostUIDRootGroup returns "uid:0" for the current host user -- the
// container's main process should own the bind-mounted project files as
// this host user (so a plain host-side edit/touch never needs a manual
// chown), while staying in GID 0 to keep the write access the base image
// already grants group 0 on its own runtime directories (Caddy config,
// msmtp, etc. -- see docker-php's frankenphp Dockerfile). Falls back to
// empty (container/image default) if detection fails.
func hostUIDRootGroup() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s:0", u.Uid)
}

// createContainer creates a new container for the project
func (m *Manager) createContainer(project *Project) error {
	networkConfig := config.GetNetworkConfig()

	// Ensure network exists
	if err := m.docker.CreateNetwork(networkConfig.BridgeName); err != nil {
		return fmt.Errorf("failed to create network: %w", err)
	}

	image, err := m.resolveImage(project)
	if err != nil {
		return err
	}

	// Resolve the DNS container's live IP on the internal network rather than
	// relying on a hardcoded address that may belong to a different container.
	dnsServers := []string{"1.1.1.1", "8.8.8.8"} // fallback to public resolvers
	if dnsIP, err := m.docker.GetContainerIP("ingot_dns", networkConfig.BridgeName); err == nil && dnsIP != "" {
		dnsServers = []string{dnsIP, "1.1.1.1", "8.8.8.8"}
	} else {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Could not resolve ingot_dns IP (%v); falling back to public DNS. .test domains will not resolve.\n", err)
	}

	// Inject the project's user env vars (`ingot env set`) the same way
	// production supplies a site's config: plain KEY=VALUE env vars on the
	// container (see RunDockerContainer's buildEnvironmentVariables in the
	// Alios platform repo), just sourced from the container label instead of
	// a database-backed setting. Ingot's own infra vars are set after them
	// so they always win - mirrors production setting SERVER_ROOT last,
	// specifically so a stray env var can never silently override it (SetEnv
	// also rejects these keys up front). PORT matters beyond the prebuilt
	// image's own defaults: a custom Dockerfile's baked Caddyfile can
	// reference {$PORT} with no fallback of its own (production always
	// supplies it - see buildDockerCommand's `-e PORT=...` - so its own
	// Caddyfiles are never written to expect a default), and an empty
	// substitution there is a hard Caddyfile parse failure, not a graceful
	// fallback.
	envVars := make(map[string]string, len(project.Env)+4)
	for key, value := range project.Env {
		envVars[key] = value
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
		User:     hostUIDRootGroup(),
		Volumes: []string{
			fmt.Sprintf("%s:/app", project.Path),
		},
		Environment: envVars,
		Labels: map[string]string{
			"ingot.project.name":            project.Name,
			"ingot.project.path":            project.Path,
			"ingot.project.php_version":     project.PHPVersion,
			"ingot.project.server_root":     project.ServerRoot,
			"ingot.project.image_type":      project.ImageType,
			"ingot.project.image_source":    project.ImageSource,
			"ingot.project.image_url":       project.ImageURL,
			"ingot.project.dockerfile_path": project.DockerfilePath,
			"ingot.project.build_context":   project.BuildContext,
			"ingot.project.build_args":      serializeBuildArgs(project.BuildArgs),
			"ingot.project.framework":       project.Framework,
			envLabel:                        serializeEnv(project.Env),
			idle.IdleLabel:                  project.Idle,
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
	return fmt.Sprintf("ingot_php_%s", projectName)
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
	containers, err := m.docker.ListContainers("label=ingot.service.type=database")
	if err != nil {
		return nil, err
	}

	var databases []string
	for _, container := range containers {
		// Extract database name from container name (ingot_mysql -> mysql)
		if strings.HasPrefix(container, "ingot_") && !strings.Contains(container, "_caddy") {
			dbName := strings.TrimPrefix(container, "ingot_")
			databases = append(databases, dbName)
		}
	}

	return databases, nil
}
