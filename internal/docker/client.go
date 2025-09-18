package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Client provides a high-level interface to Docker operations
type Client struct {
	mu            sync.RWMutex
	dockerPath    string
	composePath   string
	isAvailable   bool
	lastCheckTime time.Time
}

// NewClient creates a new Docker client
func NewClient() (*Client, error) {
	client := &Client{}

	// Find docker binary
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("docker not found: %w", err)
	}
	client.dockerPath = dockerPath

	// Find docker compose
	composePath, err := exec.LookPath("docker")
	if err != nil {
		return nil, fmt.Errorf("docker compose not found: %w", err)
	}
	client.composePath = composePath

	// Verify Docker is running
	if err := client.CheckAvailability(); err != nil {
		return nil, fmt.Errorf("docker is not running: %w", err)
	}

	return client, nil
}

// CheckAvailability verifies that Docker daemon is accessible
func (c *Client) CheckAvailability() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Cache availability check for 5 seconds
	if time.Since(c.lastCheckTime) < 5*time.Second && c.isAvailable {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.dockerPath, "version", "--format", "json")
	output, err := cmd.Output()
	if err != nil {
		c.isAvailable = false
		return fmt.Errorf("docker daemon not responding: %w", err)
	}

	// Verify we got valid JSON response
	var version map[string]interface{}
	if err := json.Unmarshal(output, &version); err != nil {
		c.isAvailable = false
		return fmt.Errorf("invalid docker response: %w", err)
	}

	c.isAvailable = true
	c.lastCheckTime = time.Now()
	return nil
}

// Run executes a docker command without returning output
func (c *Client) Run(args ...string) error {
	if err := c.CheckAvailability(); err != nil {
		return err
	}

	cmd := exec.Command(c.dockerPath, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s failed: %w", args[0], err)
	}

	return nil
}

// RunWithOutput executes a docker command and returns output
func (c *Client) RunWithOutput(args ...string) (string, error) {
	if err := c.CheckAvailability(); err != nil {
		return "", err
	}

	cmd := exec.Command(c.dockerPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s failed: %w\nOutput: %s", args[0], err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

// RunInteractive executes a docker command with stdin/stdout/stderr attached
func (c *Client) RunInteractive(args ...string) error {
	if err := c.CheckAvailability(); err != nil {
		return err
	}

	cmd := exec.Command(c.dockerPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s failed: %w", args[0], err)
	}

	return nil
}

// StreamOutput executes a docker command and streams output line by line
func (c *Client) StreamOutput(callback func(string), args ...string) error {
	if err := c.CheckAvailability(); err != nil {
		return err
	}

	cmd := exec.Command(c.dockerPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	var wg sync.WaitGroup

	// Stream stdout
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			callback(scanner.Text())
		}
	}()

	// Stream stderr
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			callback(scanner.Text())
		}
	}()

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("docker %s failed: %w", args[0], err)
	}

	return nil
}

// ContainerInfo holds information about a Docker container
type ContainerInfo struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Image      string            `json:"image"`
	Status     string            `json:"status"`
	State      string            `json:"state"`
	Labels     map[string]string `json:"labels"`
	Ports      []PortMapping     `json:"ports"`
	Networks   []string          `json:"networks"`
	CreatedAt  time.Time         `json:"created_at"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at,omitempty"`
}

// PortMapping represents a container port mapping
type PortMapping struct {
	ContainerPort string `json:"container_port"`
	HostPort      string `json:"host_port"`
	Protocol      string `json:"protocol"`
}

// InspectContainer returns detailed information about a container
func (c *Client) InspectContainer(name string) (*ContainerInfo, error) {
	output, err := c.RunWithOutput("inspect", name)
	if err != nil {
		if strings.Contains(err.Error(), "No such object") {
			return nil, ErrContainerNotFound
		}
		return nil, err
	}

	var containers []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		State struct {
			Status     string    `json:"Status"`
			Running    bool      `json:"Running"`
			StartedAt  time.Time `json:"StartedAt"`
			FinishedAt time.Time `json:"FinishedAt"`
		} `json:"State"`
		NetworkSettings struct {
			Networks map[string]interface{} `json:"Networks"`
		} `json:"NetworkSettings"`
		Created time.Time `json:"Created"`
	}

	if err := json.Unmarshal([]byte(output), &containers); err != nil {
		return nil, fmt.Errorf("failed to parse container info: %w", err)
	}

	if len(containers) == 0 {
		return nil, ErrContainerNotFound
	}

	container := containers[0]
	info := &ContainerInfo{
		ID:        container.ID,
		Name:      strings.TrimPrefix(container.Name, "/"),
		Image:     container.Config.Image,
		Status:    container.State.Status,
		Labels:    container.Config.Labels,
		CreatedAt: container.Created,
		StartedAt: container.State.StartedAt,
	}

	if container.State.Running {
		info.State = "running"
	} else {
		info.State = "stopped"
		info.FinishedAt = container.State.FinishedAt
	}

	// Extract networks
	for name := range container.NetworkSettings.Networks {
		info.Networks = append(info.Networks, name)
	}

	return info, nil
}

// ContainerExists checks if a container exists
func (c *Client) ContainerExists(name string) bool {
	_, err := c.InspectContainer(name)
	return err == nil
}

// IsContainerRunning checks if a container is running
func (c *Client) IsContainerRunning(name string) bool {
	info, err := c.InspectContainer(name)
	if err != nil {
		return false
	}
	return info.State == "running"
}

// StartContainer starts a stopped container
func (c *Client) StartContainer(name string) error {
	if !c.ContainerExists(name) {
		return ErrContainerNotFound
	}

	if c.IsContainerRunning(name) {
		return nil // Already running
	}

	return c.Run("start", name)
}

// StopContainer stops a running container
func (c *Client) StopContainer(name string, timeout int) error {
	if !c.ContainerExists(name) {
		return ErrContainerNotFound
	}

	if !c.IsContainerRunning(name) {
		return nil // Already stopped
	}

	args := []string{"stop"}
	if timeout > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", timeout))
	}
	args = append(args, name)

	return c.Run(args...)
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(name string, force bool) error {
	if !c.ContainerExists(name) {
		return nil // Already removed
	}

	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	args = append(args, name)

	return c.Run(args...)
}

// ForceRemoveContainer forcefully removes a container, ignoring errors if it doesn't exist
func (c *Client) ForceRemoveContainer(name string) error {
	// Always attempt force removal, don't check if exists first
	// This ensures we try to clean up even partially created containers
	return c.Run("rm", "-f", name)
}

// CreateNetwork creates a Docker network
func (c *Client) CreateNetwork(name string, options ...string) error {
	// Check if network already exists
	if err := c.Run("network", "inspect", name); err == nil {
		return nil // Network already exists
	}

	args := []string{"network", "create"}
	args = append(args, options...)
	args = append(args, name)

	return c.Run(args...)
}

// RemoveNetwork removes a Docker network
func (c *Client) RemoveNetwork(name string) error {
	return c.Run("network", "rm", name)
}

// CreateVolume creates a Docker volume
func (c *Client) CreateVolume(name string) error {
	// Check if volume already exists
	if err := c.Run("volume", "inspect", name); err == nil {
		return nil // Volume already exists
	}

	return c.Run("volume", "create", name)
}

// RemoveVolume removes a Docker volume
func (c *Client) RemoveVolume(name string) error {
	return c.Run("volume", "rm", name)
}

// RunContainer runs a new container with the specified configuration
func (c *Client) RunContainer(config *ContainerConfig) error {
	args := []string{"run"}

	if config.Detached {
		args = append(args, "-d")
	}

	if config.Name != "" {
		args = append(args, "--name", config.Name)
	}

	if config.Restart != "" {
		args = append(args, "--restart", config.Restart)
	}

	if config.Network != "" {
		args = append(args, "--network", config.Network)
	}

	if config.Platform != "" {
		args = append(args, "--platform", config.Platform)
	}

	// Add volumes
	for _, volume := range config.Volumes {
		args = append(args, "-v", volume)
	}

	// Add ports
	for _, port := range config.Ports {
		args = append(args, "-p", port)
	}

	// Add environment variables
	for key, value := range config.Environment {
		args = append(args, "-e", fmt.Sprintf("%s=%s", key, value))
	}

	// Add labels
	for key, value := range config.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", key, value))
	}

	// Add DNS servers
	for _, dns := range config.DNS {
		args = append(args, "--dns", dns)
	}

	args = append(args, config.Image)

	// Add command if specified
	if len(config.Command) > 0 {
		args = append(args, config.Command...)
	}

	return c.Run(args...)
}

// ContainerConfig holds configuration for running a container
type ContainerConfig struct {
	Name        string
	Image       string
	Detached    bool
	Restart     string
	Network     string
	Platform    string
	Volumes     []string
	Ports       []string
	Environment map[string]string
	Labels      map[string]string
	DNS         []string
	Command     []string
}

// Compose runs docker compose with the provided compose content
func (c *Client) Compose(composeContent string, args ...string) error {
	cmd := exec.Command(c.dockerPath, append([]string{"compose", "-f", "-"}, args...)...)
	cmd.Stdin = strings.NewReader(composeContent)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose failed: %w", err)
	}

	return nil
}

// ListContainers lists containers matching the filter
func (c *Client) ListContainers(filter string) ([]string, error) {
	args := []string{"ps", "-a", "--format", "{{.Names}}"}
	if filter != "" {
		args = append(args, "--filter", filter)
	}

	output, err := c.RunWithOutput(args...)
	if err != nil {
		return nil, err
	}

	if output == "" {
		return []string{}, nil
	}

	return strings.Split(output, "\n"), nil
}

// PullImage pulls a Docker image
func (c *Client) PullImage(image string, callback func(string)) error {
	if callback != nil {
		return c.StreamOutput(callback, "pull", image)
	}
	return c.Run("pull", image)
}

// ExecCommand executes a command in a running container
func (c *Client) ExecCommand(container string, interactive bool, command ...string) error {
	args := []string{"exec"}

	if interactive {
		args = append(args, "-it")
		args = append(args, container)
		args = append(args, command...)
		return c.RunInteractive(args...)
	}

	args = append(args, container)
	args = append(args, command...)
	return c.Run(args...)
}

// CopyToContainer copies data to a container
func (c *Client) CopyToContainer(container, destination string, data []byte) error {
	cmd := exec.Command(c.dockerPath, "exec", "-i", container, "tee", destination)
	cmd.Stdin = bytes.NewReader(data)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy to container: %w", err)
	}

	return nil
}

// Errors
var (
	ErrContainerNotFound = fmt.Errorf("container not found")
	ErrDockerNotRunning  = fmt.Errorf("docker daemon is not running")
)
