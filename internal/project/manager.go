package project

import (
	"github.com/aliasproject/devbox/internal/utils"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
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

// Project represents a Devbox project
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
				Name:       name,
				Path:       cwd,
				ServerRoot: "public", // default for new projects
				Status:     "not initialized",
				Container:  container,
			}, nil
		}
		return nil, err
	}

	// Get configuration from Docker labels
	phpVersion := info.Labels["devbox.project.php_version"]
	if phpVersion == "" {
		phpVersion = config.DefaultPHPVersion
	}

	serverRoot := info.Labels["devbox.project.server_root"]
	if serverRoot == "" {
		serverRoot = "public" // default
	}

	status := "stopped"
	if info.State == "running" {
		status = "running"
	}

	return &Project{
		Name:       name,
		Path:       cwd,
		PHPVersion: phpVersion,
		ServerRoot: serverRoot,
		Status:     status,
		Container:  container,
		URL:        fmt.Sprintf("https://%s.test", name),
		CreatedAt:  info.CreatedAt,
		UpdatedAt:  info.StartedAt,
	}, nil
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

// Start starts the project container with optional PHP version
func (m *Manager) Start(phpVersion string, serverRoot ...string) error {
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

	// Check if container exists
	if m.docker.ContainerExists(project.Container) {
		// Check if existing container has correct PHP version
		info, err := m.docker.InspectContainer(project.Container)
		if err == nil {
			existingVersion := info.Labels["devbox.project.php_version"]
			if existingVersion != "" && existingVersion != desiredPHPVersion {
				// PHP version mismatch - recreate container
				fmt.Printf(utils.Colorize("[RESTARTING]", utils.ColorCyan) + " PHP version changed from %s to %s, recreating container...\n", existingVersion, desiredPHPVersion)
				if err := m.docker.RemoveContainer(project.Container, true); err != nil {
					return fmt.Errorf("failed to remove old container: %w", err)
				}
				project.PHPVersion = desiredPHPVersion
				if err := m.createContainer(project); err != nil {
					return fmt.Errorf("failed to create container with new PHP version: %w", err)
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
				fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: Failed to cleanup container after creation failure: %v\n", cleanupErr)
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
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: Failed to remove container normally: %v\n", err)

		// Try one more time with direct docker command
		if forcedErr := m.docker.Run("rm", "-f", project.Container); forcedErr != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: Forced removal also failed: %v\n", forcedErr)
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

// List returns all Devbox projects
func (m *Manager) List() ([]*Project, error) {
	containers, err := m.docker.ListContainers("label=devbox.project.name")
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

		name := info.Labels["devbox.project.name"]
		if name == "" {
			name = strings.TrimPrefix(containerName, "devbox_php_")
		}

		phpVersion := info.Labels["devbox.project.php_version"]
		if phpVersion == "" {
			phpVersion = config.DefaultPHPVersion
		}

		status := "stopped"
		if info.State == "running" {
			status = "running"
		}

		projects = append(projects, &Project{
			Name:       name,
			Path:       info.Labels["devbox.project.path"],
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

	err = m.docker.ExecCommand(project.Container, interactive, command...)
	if err != nil {
		// Check for common error patterns and provide better messages
		errStr := err.Error()
		if strings.Contains(errStr, "executable file not found in $PATH") {
			cmdName := ""
			if len(command) > 0 {
				cmdName = command[0]
			}
			return fmt.Errorf("command '%s' not found in container. Try 'devbox shell' to explore available commands", cmdName)
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

// OpenShell opens an interactive shell in the project container
func (m *Manager) OpenShell() error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	if project.Status != "running" {
		return fmt.Errorf("project container is not running")
	}

	// Use sh which is POSIX-compliant and available in all containers
	return m.docker.ExecCommand(project.Container, true, "sh")
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

	// Pull the image first
	image := fmt.Sprintf("ghcr.io/aliasproject/php:%s-frankenphp-dev", project.PHPVersion)
	if err := m.docker.PullImage(image, nil); err != nil {
		// Continue even if pull fails - image might exist locally
		fmt.Printf("Warning: Failed to pull image: %v\n", err)
	}

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
		Environment: map[string]string{
			"SERVER_NAME": ":80",
			"SERVER_ROOT": project.ServerRoot,
			"HTTPS":       "on",
		},
		Labels: map[string]string{
			"devbox.project.name":        project.Name,
			"devbox.project.path":        project.Path,
			"devbox.project.php_version": project.PHPVersion,
			"devbox.project.server_root": project.ServerRoot,
		},
		DNS: []string{networkConfig.DNSServer},
	}

	// No additional configuration needed - everything is ephemeral or in Docker labels

	if err := m.docker.RunContainer(containerConfig); err != nil {
		// Best effort cleanup on failure - remove any partially created container
		cleanupErr := m.docker.RemoveContainer(project.Container, true)
		if cleanupErr != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow) + " Warning: Failed to cleanup container after run failure: %v\n", cleanupErr)
		}
		return fmt.Errorf("failed to run container: %w", err)
	}

	return nil
}

// getContainerName returns the container name for a project
func (m *Manager) getContainerName(projectName string) string {
	return fmt.Sprintf("devbox_php_%s", projectName)
}

// ValidatePHPVersion checks if a PHP version is valid
func ValidatePHPVersion(version string) bool {
	validVersions := []string{"8.3", "8.2", "8.1", "8.0", "7.4"}
	for _, v := range validVersions {
		if v == version {
			return true
		}
	}
	return false
}

// GetAvailablePHPVersions returns available PHP versions
func GetAvailablePHPVersions() []string {
	return []string{"8.3", "8.2", "8.1", "8.0", "7.4"}
}

// getRunningDatabases returns a list of currently running database services
func (m *Manager) getRunningDatabases() ([]string, error) {
	containers, err := m.docker.ListContainers("label=devbox.service.type=database")
	if err != nil {
		return nil, err
	}

	var databases []string
	for _, container := range containers {
		// Extract database name from container name (devbox_mysql -> mysql)
		if strings.HasPrefix(container, "devbox_") && !strings.Contains(container, "_nginx") && !strings.Contains(container, "_mailhog") {
			dbName := strings.TrimPrefix(container, "devbox_")
			databases = append(databases, dbName)
		}
	}

	return databases, nil
}
