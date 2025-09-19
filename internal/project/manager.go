package project

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
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
				Name:      name,
				Path:      cwd,
				Status:    "not initialized",
				Container: container,
			}, nil
		}
		return nil, err
	}

	phpVersion := info.Labels["devbox.project.php_version"]
	if phpVersion == "" {
		phpVersion = config.DefaultPHPVersion
	}

	status := "stopped"
	if info.State == "running" {
		status = "running"
	}

	return &Project{
		Name:       name,
		Path:       cwd,
		PHPVersion: phpVersion,
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

	// Save project configuration in central location
	projectConfig := &config.ProjectConfig{
		Name:        project.Name,
		Path:        project.Path,
		PHPVersion:  phpVersion,
		Environment: make(map[string]string),
		Ports:       make(map[string]string),
		CreatedAt:   time.Now().Format(time.RFC3339),
		UpdatedAt:   time.Now().Format(time.RFC3339),
	}

	if err := m.saveProjectConfig(project.Path, projectConfig); err != nil {
		return fmt.Errorf("failed to save project config: %w", err)
	}

	return nil
}

// Start starts the project container with optional PHP version
func (m *Manager) Start(phpVersion ...string) error {
	project, err := m.GetCurrentProject()
	if err != nil {
		return err
	}

	// Determine PHP version to use
	var desiredPHPVersion string
	if len(phpVersion) > 0 && phpVersion[0] != "" {
		desiredPHPVersion = phpVersion[0]
	} else {
		// Load from saved config if exists
		projectConfig, _ := m.loadProjectConfig(project.Path)
		if projectConfig != nil {
			desiredPHPVersion = projectConfig.PHPVersion
		}
	}

	// Use default if no version specified
	if desiredPHPVersion == "" {
		desiredPHPVersion = m.config.DefaultPHPVersion
	}

	// Check if container exists
	if m.docker.ContainerExists(project.Container) {
		// Check if existing container has correct PHP version
		info, err := m.docker.InspectContainer(project.Container)
		if err == nil {
			existingVersion := info.Labels["devbox.project.php_version"]
			if existingVersion != "" && existingVersion != desiredPHPVersion {
				// PHP version mismatch - recreate container
				fmt.Printf("🔄 PHP version changed from %s to %s, recreating container...\n", existingVersion, desiredPHPVersion)
				if err := m.docker.RemoveContainer(project.Container, true); err != nil {
					return fmt.Errorf("failed to remove old container: %w", err)
				}
				project.PHPVersion = desiredPHPVersion
				if err := m.createContainer(project); err != nil {
					return fmt.Errorf("failed to create container with new PHP version: %w", err)
				}
				// Save the new configuration
				m.saveProjectConfigSimple(project.Path, desiredPHPVersion)
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
		if err := m.createContainer(project); err != nil {
			// Clean up any partially created container on failure
			cleanupErr := m.docker.RemoveContainer(project.Container, true)
			if cleanupErr != nil {
				// Log but don't fail on cleanup error
				fmt.Printf("⚠️  Warning: Failed to cleanup container after creation failure: %v\n", cleanupErr)
			}
			return fmt.Errorf("failed to create container: %w", err)
		}
		// Save the configuration
		m.saveProjectConfigSimple(project.Path, desiredPHPVersion)
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
		fmt.Printf("⚠️  Warning: Failed to remove container normally: %v\n", err)

		// Try one more time with direct docker command
		if forcedErr := m.docker.Run("rm", "-f", project.Container); forcedErr != nil {
			fmt.Printf("⚠️  Warning: Forced removal also failed: %v\n", forcedErr)
		}
	}

	// Remove project configuration from central location
	m.removeProjectConfig(project.Path)

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

	// Update project configuration in central location
	projectConfig, _ := m.loadProjectConfig(project.Path)
	if projectConfig == nil {
		projectConfig = &config.ProjectConfig{
			Name: project.Name,
			Path: project.Path,
		}
	}
	projectConfig.PHPVersion = newVersion
	projectConfig.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := m.saveProjectConfig(project.Path, projectConfig); err != nil {
		return fmt.Errorf("failed to update project config: %w", err)
	}

	// Create new container with new PHP version
	project.PHPVersion = newVersion
	if err := m.createContainer(project); err != nil {
		return fmt.Errorf("failed to create new container: %w", err)
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

	// Load project configuration from central location
	projectConfig, err := m.loadProjectConfig(project.Path)
	if err == nil && projectConfig != nil {
		status.Databases = projectConfig.Databases
		status.Environment = projectConfig.Environment
		status.Ports = projectConfig.Ports
	}

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
		},
		Labels: map[string]string{
			"devbox.project.name":        project.Name,
			"devbox.project.path":        project.Path,
			"devbox.project.php_version": project.PHPVersion,
		},
		DNS: []string{networkConfig.DNSServer},
	}

	// Load project configuration for additional settings from central location
	projectConfig, err := m.loadProjectConfig(project.Path)
	if err == nil && projectConfig != nil {
		// Add custom environment variables
		for key, value := range projectConfig.Environment {
			containerConfig.Environment[key] = value
		}

		// Add custom ports
		for _, port := range projectConfig.Ports {
			containerConfig.Ports = append(containerConfig.Ports, port)
		}
	}

	if err := m.docker.RunContainer(containerConfig); err != nil {
		// Best effort cleanup on failure - remove any partially created container
		cleanupErr := m.docker.RemoveContainer(project.Container, true)
		if cleanupErr != nil {
			fmt.Printf("⚠️  Warning: Failed to cleanup container after run failure: %v\n", cleanupErr)
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

// getProjectConfigPath returns the central config path for a project
func (m *Manager) getProjectConfigPath(projectPath string) string {
	// Create a unique identifier from the project path
	hash := md5.Sum([]byte(projectPath))
	hashStr := hex.EncodeToString(hash[:])

	// Store in ~/.devbox/projects/<hash>.json
	configDir := filepath.Join(m.config.ConfigDir, "projects")
	os.MkdirAll(configDir, 0755)

	return filepath.Join(configDir, hashStr+".json")
}

// loadProjectConfig loads project configuration from central location
func (m *Manager) loadProjectConfig(projectPath string) (*config.ProjectConfig, error) {
	configFile := m.getProjectConfigPath(projectPath)

	if _, err := os.Stat(configFile); os.IsNotExist(err) {
		return nil, nil // No project config yet
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read project config: %w", err)
	}

	var projectConfig config.ProjectConfig
	if err := json.Unmarshal(data, &projectConfig); err != nil {
		return nil, fmt.Errorf("failed to parse project config: %w", err)
	}

	return &projectConfig, nil
}

// saveProjectConfig saves project configuration to central location
func (m *Manager) saveProjectConfig(projectPath string, config *config.ProjectConfig) error {
	configFile := m.getProjectConfigPath(projectPath)

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project config: %w", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write project config: %w", err)
	}

	return nil
}

// saveProjectConfigSimple saves just the PHP version for a project
func (m *Manager) saveProjectConfigSimple(projectPath string, phpVersion string) error {
	projectConfig, _ := m.loadProjectConfig(projectPath)
	if projectConfig == nil {
		projectConfig = &config.ProjectConfig{
			Name:        filepath.Base(projectPath),
			Path:        projectPath,
			Environment: make(map[string]string),
			Ports:       make(map[string]string),
			CreatedAt:   time.Now().Format(time.RFC3339),
		}
	}
	projectConfig.PHPVersion = phpVersion
	projectConfig.UpdatedAt = time.Now().Format(time.RFC3339)

	return m.saveProjectConfig(projectPath, projectConfig)
}

// removeProjectConfig removes project configuration from central location
func (m *Manager) removeProjectConfig(projectPath string) error {
	configFile := m.getProjectConfigPath(projectPath)
	return os.Remove(configFile)
}
