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
	databases := m.config.GetDatabases()
	for name := range databases {
		displayName := name
		dbContainer := "devbox_db_" + name
		service := &Service{
			Name:      displayName,
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
	databases := m.config.GetDatabases()
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

	// Add databases
	for name, dbConfig := range databases {
		if dbConfig.Type == "mysql" {
			compose += fmt.Sprintf(`
  db_%s:
    container_name: devbox_db_%s
    image: mysql:%s
    platform: linux/amd64
    restart: unless-stopped
    environment:
      - MYSQL_ROOT_PASSWORD=
      - MYSQL_ALLOW_EMPTY_PASSWORD=yes
    ports:
      - "%s:3306"
    networks:
      - devbox-network
      - internal
    volumes:
      - db_%s:/var/lib/mysql
`, name, name, dbConfig.Version, dbConfig.Port, name)
		} else if dbConfig.Type == "postgresql" {
			compose += fmt.Sprintf(`
  db_%s:
    container_name: devbox_db_%s
    image: postgres:%s
    restart: unless-stopped
    environment:
      - POSTGRES_USER=devbox
      - POSTGRES_PASSWORD=devbox
      - POSTGRES_DB=devbox
    ports:
      - "%s:5432"
    networks:
      - devbox-network
      - internal
    volumes:
      - db_%s:/var/lib/postgresql/data
`, name, name, dbConfig.Version, dbConfig.Port, name)
		}
	}

	// Add volumes
	compose += `
volumes:
  certs:
    name: devbox_certs
  vhosts:
    name: devbox_vhosts`

	// Add volumes for each database
	for name := range databases {
		compose += fmt.Sprintf(`
  db_%s:
    name: devbox_db_%s`, name, name)
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
func (m *Manager) AddDatabase(name, dbType, version string) error {
	m.mu.Lock()

	// Validate database type
	if dbType != "mysql" && dbType != "postgresql" {
		m.mu.Unlock()
		return fmt.Errorf("invalid database type: %s", dbType)
	}

	// Check if name already exists
	databases := m.config.GetDatabases()
	if _, exists := databases[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("database '%s' already exists", name)
	}

	// Find an available port
	port := m.findAvailablePort(dbType, databases)

	// Save configuration
	if err := m.config.AddDatabase(name, dbType, version, port); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("failed to save database config: %w", err)
	}

	// Create volume
	volumeName := fmt.Sprintf("devbox_db_%s", name)
	if err := m.docker.CreateVolume(volumeName); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("failed to create volume: %w", err)
	}

	// Display name for output
	displayName := "MySQL"
	if dbType == "postgresql" {
		displayName = "PostgreSQL"
	}

	fmt.Printf("✅ %s '%s' (version %s) configured on port %s\n", displayName, name, version, port)
	fmt.Println("Restarting global services to apply changes...")

	// Release mutex before calling Restart to avoid deadlock
	m.mu.Unlock()

	// Restart services to apply the new database configuration
	// The database will be added to the compose group
	return m.Restart()
}

// findAvailablePort finds an available port for the database
func (m *Manager) findAvailablePort(dbType string, databases map[string]config.DatabaseConfig) string {
	basePort := 3306
	if dbType == "postgresql" {
		basePort = 5432
	}

	usedPorts := make(map[int]bool)
	for _, db := range databases {
		var port int
		fmt.Sscanf(db.Port, "%d", &port)
		usedPorts[port] = true
	}

	// Find first available port starting from base
	for port := basePort; port < basePort+100; port++ {
		if !usedPorts[port] {
			return fmt.Sprintf("%d", port)
		}
	}

	// Fallback to a random high port
	return fmt.Sprintf("%d", basePort+100)
}

// RemoveDatabase removes a database service
func (m *Manager) RemoveDatabase(name string) error {
	m.mu.Lock()

	databases := m.config.GetDatabases()
	dbConfig, exists := databases[name]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("database '%s' not found", name)
	}

	// Clear configuration
	if err := m.config.RemoveDatabase(name); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("failed to update config: %w", err)
	}

	// Display name for output
	displayName := "MySQL"
	if dbConfig.Type == "postgresql" {
		displayName = "PostgreSQL"
	}

	fmt.Printf("Removing %s '%s'...\n", displayName, name)

	// Release mutex before calling Restart to avoid deadlock
	m.mu.Unlock()

	// Restart services to remove the database from compose group
	return m.Restart()
}

// GetDatabases returns list of database services
func (m *Manager) GetDatabases() ([]*Service, error) {
	services := make([]*Service, 0)
	databases := m.config.GetDatabases()

	for name, dbConfig := range databases {
		container := "devbox_db_" + name

		service := &Service{
			Name:      name,
			Container: container,
			Status:    "not found",
		}

		// Get actual container info
		info, err := m.docker.InspectContainer(container)
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
		} else {
			// Database configured but not running
			if dbConfig.Type == "mysql" {
				service.Image = fmt.Sprintf("mysql:%s", dbConfig.Version)
			} else {
				service.Image = fmt.Sprintf("postgres:%s", dbConfig.Version)
			}
			service.Ports = []string{fmt.Sprintf("%s:*", dbConfig.Port)}
		}

		services = append(services, service)
	}

	return services, nil
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
