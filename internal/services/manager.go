package services

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
)

// Manager handles global services operations
type Manager struct {
	docker *docker.Client
	config *config.Config
	mu     sync.RWMutex
}

// NewManager creates a new services manager
func NewManager(dockerClient *docker.Client) *Manager {
	return &Manager{
		docker: dockerClient,
		config: config.Get(),
	}
}

// Service represents a global service
type Service struct {
	Name      string
	Container string
	Image     string
	Status    string
	Ports     []string
	Health    string
}

// GlobalServices defines the core global services
var GlobalServices = []string{
	"devbox_dns",
	"devbox_nginx",
	"devbox_mail",
}

// Start starts all global services
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get host IP for DNS configuration
	hostIP, err := GetHostIP()
	if err != nil {
		hostIP = "127.0.0.1"
	}

	// Generate compose configuration
	composeContent := m.generateComposeConfig(hostIP)

	// Start services using docker compose
	if err := m.docker.Compose(composeContent, "up", "-d"); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Wait for services to be ready
	if err := m.waitForServices(30 * time.Second); err != nil {
		return fmt.Errorf("services failed to start: %w", err)
	}

	return nil
}

// Stop stops all global services
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Generate minimal compose config for stopping
	composeContent := `name: devbox
services: {}`

	if err := m.docker.Compose(composeContent, "down"); err != nil {
		return fmt.Errorf("failed to stop services: %w", err)
	}

	return nil
}

// Restart restarts all global services
func (m *Manager) Restart() error {
	if err := m.Stop(); err != nil {
		return err
	}

	// Wait a moment for clean shutdown
	time.Sleep(2 * time.Second)

	return m.Start()
}

// Status returns the status of all global services
func (m *Manager) Status() ([]*Service, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	services := make([]*Service, 0)

	// Check core services
	for _, containerName := range GlobalServices {
		service := &Service{
			Name:      strings.TrimPrefix(containerName, "devbox_"),
			Container: containerName,
			Status:    "not found",
		}

		info, err := m.docker.InspectContainer(containerName)
		if err == nil {
			service.Status = info.State
			service.Image = info.Image

			// Extract ports
			for _, port := range info.Ports {
				portStr := fmt.Sprintf("%s:%s", port.HostPort, port.ContainerPort)
				service.Ports = append(service.Ports, portStr)
			}

			// Check health
			if info.State == "running" {
				service.Health = "healthy"
			} else {
				service.Health = "unhealthy"
			}
		}

		services = append(services, service)
	}

	// Check database services
	dbType, _ := m.config.GetDatabase()
	if dbType != "" {
		dbContainer := "devbox_" + dbType
		service := &Service{
			Name:      dbType,
			Container: dbContainer,
			Status:    "not found",
		}

		info, err := m.docker.InspectContainer(dbContainer)
		if err == nil {
			service.Status = info.State
			service.Image = info.Image

			// Extract ports
			for _, port := range info.Ports {
				portStr := fmt.Sprintf("%s:%s", port.HostPort, port.ContainerPort)
				service.Ports = append(service.Ports, portStr)
			}

			if info.State == "running" {
				service.Health = "healthy"
			} else {
				service.Health = "unhealthy"
			}
		}

		services = append(services, service)
	}

	return services, nil
}

// IsRunning checks if all core global services are running
func (m *Manager) IsRunning() bool {
	for _, container := range GlobalServices {
		if !m.docker.IsContainerRunning(container) {
			return false
		}
	}
	return true
}

// EnsureRunning ensures global services are running, starting them if needed
func (m *Manager) EnsureRunning() error {
	if !m.IsRunning() {
		return m.Start()
	}
	return nil
}

// waitForServices waits for services to be ready
func (m *Manager) waitForServices(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		allRunning := true
		for _, container := range GlobalServices {
			if !m.docker.IsContainerRunning(container) {
				allRunning = false
				break
			}
		}

		if allRunning {
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for services")
}

// generateComposeConfig generates docker-compose configuration
func (m *Manager) generateComposeConfig(hostIP string) string {
	dbType, dbVersion := m.config.GetDatabase()
	networkConfig := config.GetNetworkConfig()

	compose := fmt.Sprintf(`name: devbox
services:
  dns:
    container_name: devbox_dns
    image: ghcr.io/aliasproject/dns:latest
    restart: unless-stopped
    ports:
      - "0.0.0.0:5354:53/udp"
    networks:
      internal:
        ipv4_address: %s
    environment:
      - COMMANDS=--address=/test/%s --server=1.1.1.1 --server=8.8.8.8 --log-facility=-
    dns:
      - 1.1.1.1
      - 8.8.8.8

  nginx:
    container_name: devbox_nginx
    image: ghcr.io/aliasproject/nginx:latest
    restart: unless-stopped
    volumes:
      - certs:/etc/nginx/ssl
      - vhosts:/etc/nginx/conf.d
    ports:
      - "80:80"
      - "443:443"
    networks:
      - devbox-network
      - internal
    depends_on:
      - dns

  mail:
    container_name: devbox_mail
    image: mailhog/mailhog:latest
    platform: linux/amd64
    restart: on-failure
    ports:
      - "1025:1025"
      - "8025:8025"
    networks:
      - internal
`, networkConfig.DNSServer, hostIP)

	// Add database if configured
	if dbType == "mysql" && dbVersion != "" {
		compose += fmt.Sprintf(`
  mysql:
    container_name: devbox_mysql
    image: mysql:%s
    platform: linux/amd64
    restart: unless-stopped
    environment:
      - MYSQL_ROOT_PASSWORD=
      - MYSQL_ALLOW_EMPTY_PASSWORD=yes
    ports:
      - "3306:3306"
    networks:
      - devbox-network
      - internal
    volumes:
      - mysql-%s:/var/lib/mysql
`, dbVersion, dbVersion)
	} else if dbType == "postgresql" && dbVersion != "" {
		compose += fmt.Sprintf(`
  postgresql:
    container_name: devbox_postgresql
    image: postgres:%s
    restart: unless-stopped
    environment:
      - POSTGRES_USER=devbox
      - POSTGRES_PASSWORD=devbox
      - POSTGRES_DB=devbox
    ports:
      - "5432:5432"
    networks:
      - devbox-network
      - internal
    volumes:
      - postgresql-%s:/var/lib/postgresql/data
`, dbVersion, dbVersion)
	}

	// Add volumes
	compose += `
volumes:
  certs:
    name: devbox_certs
  vhosts:
    name: devbox_vhosts`

	if dbType == "mysql" {
		compose += `
  mysql-8.0:
    name: devbox_mysql-8.0
  mysql-5.7:
    name: devbox_mysql-5.7`
	} else if dbType == "postgresql" {
		compose += `
  postgresql-16:
    name: devbox_postgresql-16
  postgresql-15:
    name: devbox_postgresql-15
  postgresql-14:
    name: devbox_postgresql-14
  postgresql-13:
    name: devbox_postgresql-13`
	}

	// Add networks
	compose += fmt.Sprintf(`

networks:
  internal:
    driver: bridge
    ipam:
      config:
        - subnet: %s
  devbox-network:
    driver: bridge
`, networkConfig.Subnet)

	return compose
}

// AddDatabase adds a database service
func (m *Manager) AddDatabase(dbType, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate database type
	if dbType != "mysql" && dbType != "postgresql" {
		return fmt.Errorf("invalid database type: %s", dbType)
	}

	// Stop existing database if running
	existingContainer := "devbox_" + dbType
	if m.docker.ContainerExists(existingContainer) {
		if err := m.docker.RemoveContainer(existingContainer, true); err != nil {
			return fmt.Errorf("failed to remove existing database: %w", err)
		}
	}

	// Save configuration
	if err := m.config.SetDatabase(dbType, version); err != nil {
		return fmt.Errorf("failed to save database config: %w", err)
	}

	// Create volume
	volumeName := fmt.Sprintf("devbox_%s-%s", dbType, version)
	if err := m.docker.CreateVolume(volumeName); err != nil {
		return fmt.Errorf("failed to create volume: %w", err)
	}

	// Ensure networks exist
	networkConfig := config.GetNetworkConfig()
	m.docker.CreateNetwork(networkConfig.BridgeName)
	m.docker.CreateNetwork(networkConfig.InternalNet)

	// Create container configuration
	var containerConfig *docker.ContainerConfig

	if dbType == "mysql" {
		containerConfig = &docker.ContainerConfig{
			Name:     existingContainer,
			Image:    fmt.Sprintf("mysql:%s", version),
			Platform: "linux/amd64",
			Detached: true,
			Restart:  "unless-stopped",
			Network:  networkConfig.BridgeName,
			Volumes: []string{
				fmt.Sprintf("%s:/var/lib/mysql", volumeName),
			},
			Ports: []string{"3306:3306"},
			Environment: map[string]string{
				"MYSQL_ROOT_PASSWORD":        "",
				"MYSQL_ALLOW_EMPTY_PASSWORD": "yes",
			},
		}
	} else {
		containerConfig = &docker.ContainerConfig{
			Name:     existingContainer,
			Image:    fmt.Sprintf("postgres:%s", version),
			Detached: true,
			Restart:  "unless-stopped",
			Network:  networkConfig.BridgeName,
			Volumes: []string{
				fmt.Sprintf("%s:/var/lib/postgresql/data", volumeName),
			},
			Ports: []string{"5432:5432"},
			Environment: map[string]string{
				"POSTGRES_USER":     "devbox",
				"POSTGRES_PASSWORD": "devbox",
				"POSTGRES_DB":       "devbox",
			},
		}
	}

	// Run the container
	if err := m.docker.RunContainer(containerConfig); err != nil {
		return fmt.Errorf("failed to start database: %w", err)
	}

	// Connect to internal network
	m.docker.Run("network", "connect", networkConfig.InternalNet, existingContainer)

	return nil
}

// RemoveDatabase removes a database service
func (m *Manager) RemoveDatabase(dbType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	container := "devbox_" + dbType
	if !m.docker.ContainerExists(container) {
		return fmt.Errorf("database %s not found", dbType)
	}

	// Stop and remove container
	if err := m.docker.RemoveContainer(container, true); err != nil {
		return fmt.Errorf("failed to remove database: %w", err)
	}

	// Clear configuration if this is the configured database
	currentDB, _ := m.config.GetDatabase()
	if currentDB == dbType {
		m.config.SetDatabase("", "")
	}

	return nil
}

// GetDatabases returns list of running database services
func (m *Manager) GetDatabases() ([]*Service, error) {
	databases := make([]*Service, 0)

	for _, dbType := range []string{"mysql", "postgresql"} {
		container := "devbox_" + dbType
		info, err := m.docker.InspectContainer(container)
		if err != nil {
			continue // Database not running
		}

		service := &Service{
			Name:      dbType,
			Container: container,
			Image:     info.Image,
			Status:    info.State,
		}

		// Extract ports
		for _, port := range info.Ports {
			portStr := fmt.Sprintf("%s:%s", port.HostPort, port.ContainerPort)
			service.Ports = append(service.Ports, portStr)
		}

		if info.State == "running" {
			service.Health = "healthy"
		} else {
			service.Health = "unhealthy"
		}

		databases = append(databases, service)
	}

	return databases, nil
}

// GetHostIP returns the host machine's IP address
func GetHostIP() (string, error) {
	// Try to connect to an external IP to determine the default interface
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("failed to determine host IP: %w", err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String(), nil
}
