package services

import (
	"fmt"
	"github.com/aliasproject/alios-cli/internal/utils"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/docker"
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
	"alios_dns",
	"alios_caddy",
	"alios_mail",
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

	// Write the base Caddyfile into the vhosts volume before the container ever
	// starts. The published caddy image's own baked-in Caddyfile only serves a
	// landing page and has no knowledge of per-project vhosts; mounting alios_vhosts
	// at /etc/caddy replaces that entirely, so this file must exist in the volume
	// (even empty of any project) or Caddy has nothing to run. Rewritten on every
	// start so it can't drift from what this binary expects.
	if err := m.docker.WriteFileToVolume("alios_vhosts", "Caddyfile", []byte("import /etc/caddy/*.caddyfile\n")); err != nil {
		return fmt.Errorf("failed to write base Caddyfile: %w", err)
	}

	// Generate compose configuration
	composeContent := m.generateComposeConfig(hostIP)

	// Start services using docker compose
	if err := m.docker.Compose(composeContent, "up", "-d"); err != nil {
		return fmt.Errorf("failed to start services: %w", err)
	}

	// Wait for services to be ready
	if err := m.waitForContainers(GlobalServices, 30*time.Second); err != nil {
		return fmt.Errorf("services failed to start: %w", err)
	}

	return nil
}

// Stop stops all global services
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Generate minimal compose config for stopping
	composeContent := `name: alios
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
			Name:      strings.TrimPrefix(containerName, "alios_"),
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
		dbContainer := "alios_" + name
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

// waitForContainers waits for the given containers to be running
func (m *Manager) waitForContainers(containers []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		allRunning := true
		for _, container := range containers {
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

// ListManagedContainers enumerates every global-service container (dns, caddy,
// mail, and any database), identified by the "alios_" name prefix while
// excluding project containers ("alios_php_*"). Discovery is done by name rather
// than by label or in-process config because pause/resume run as separate CLI
// invocations that don't share config.Config.Databases with each other.
func (m *Manager) ListManagedContainers() ([]string, error) {
	names, err := m.docker.ListContainers("")
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	managed := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "alios_") && !strings.HasPrefix(name, "alios_php_") {
			managed = append(managed, name)
		}
	}

	return managed, nil
}

// ListRunningContainers returns the subset of ListManagedContainers that are
// currently running.
func (m *Manager) ListRunningContainers() ([]string, error) {
	managed, err := m.ListManagedContainers()
	if err != nil {
		return nil, err
	}

	running := make([]string, 0, len(managed))
	for _, name := range managed {
		if m.docker.IsContainerRunning(name) {
			running = append(running, name)
		}
	}

	return running, nil
}

// Pause stops (without removing) every currently-running global-service
// container. Unlike Stop, this does not go through docker compose down, so
// containers restart quickly via Resume without recreation or re-pulling images.
// It is best-effort: a failure to stop one container is logged as a warning and
// does not prevent stopping the rest. It returns the container names that were
// successfully stopped.
func (m *Manager) Pause() ([]string, error) {
	running, err := m.ListRunningContainers()
	if err != nil {
		return nil, err
	}

	stopped := make([]string, 0, len(running))
	for _, name := range running {
		if err := m.docker.StopContainer(name, 10); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to stop '%s': %v\n", name, err)
			continue
		}
		stopped = append(stopped, name)
	}

	return stopped, nil
}

// Resume starts exactly the given global-service container names back up. This is
// the fast path (no recreation). If any named container no longer exists at all
// (e.g. removed out-of-band since pause), it falls back once to the full
// compose-based Start(), since StartContainer alone can't create a container that
// was never created.
func (m *Manager) Resume(containerNames []string) error {
	needsFullStart := false
	for _, name := range containerNames {
		if !m.docker.ContainerExists(name) {
			needsFullStart = true
			break
		}
	}

	if needsFullStart {
		return m.Start()
	}

	for _, name := range containerNames {
		if err := m.docker.StartContainer(name); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to start '%s': %v\n", name, err)
			continue
		}
	}

	return m.waitForContainers(containerNames, 30*time.Second)
}

// generateComposeConfig generates docker-compose configuration
func (m *Manager) generateComposeConfig(hostIP string) string {
	databases := m.config.GetDatabases()

	compose := fmt.Sprintf(`name: alios
services:
  dns:
    container_name: alios_dns
    image: ghcr.io/aliasproject/dns:latest
    restart: unless-stopped
    ports:
      - "5354:53/udp"
    networks:
      - internal
    environment:
      - COMMANDS=--address=/test/%s --server=1.1.1.1 --server=8.8.8.8 --log-facility=- --interface=* --bind-dynamic
    dns:
      - 1.1.1.1
      - 8.8.8.8

  caddy:
    container_name: alios_caddy
    image: ghcr.io/aliasproject/caddy:2.10-alpine
    restart: unless-stopped
    volumes:
      - certs:/etc/caddy/ssl
      - vhosts:/etc/caddy
    ports:
      - "80:80"
      - "443:443"
    networks:
      - internal
    depends_on:
      - dns

  mail:
    container_name: alios_mail
    image: mailhog/mailhog:latest
    platform: linux/amd64
    restart: on-failure
    ports:
      - "1025:1025"
      - "8025:8025"
    networks:
      - internal
`, hostIP)

	// Add databases
	for name, dbConfig := range databases {
		if dbConfig.Type == "mysql" {
			compose += fmt.Sprintf(`
  %s:
    container_name: alios_%s
    image: mysql:%s
    platform: linux/amd64
    restart: unless-stopped
    environment:
      - MYSQL_ROOT_PASSWORD=
      - MYSQL_ALLOW_EMPTY_PASSWORD=yes
    ports:
      - "%s:3306"
    networks:
      - internal
    volumes:
      - %s-%s:/var/lib/mysql
`, name, name, dbConfig.Version, dbConfig.Port, name, dbConfig.Version)
		} else if dbConfig.Type == "postgresql" {
			compose += fmt.Sprintf(`
  %s:
    container_name: alios_%s
    image: postgres:%s
    restart: unless-stopped
    environment:
      - POSTGRES_USER=alios
      - POSTGRES_PASSWORD=alios
      - POSTGRES_DB=alios
    ports:
      - "%s:5432"
    networks:
      - internal
    volumes:
      - %s-%s:/var/lib/postgresql/data
`, name, name, dbConfig.Version, dbConfig.Port, name, dbConfig.Version)
		}
	}

	// Add volumes
	compose += `
volumes:
  certs:
    name: alios_certs
  vhosts:
    name: alios_vhosts`

	// Add volumes for each database
	for name, dbConfig := range databases {
		compose += fmt.Sprintf(`
  %s-%s:
    name: alios_%s-%s`, name, dbConfig.Version, name, dbConfig.Version)
	}

	// Add networks
	compose += `

networks:
  internal:
    driver: bridge
`

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

	// Check if database type already exists (since name is now the database type)
	databases := m.config.GetDatabases()
	if _, exists := databases[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("database type '%s' is already configured", dbType)
	}

	// Find an available port
	port := m.findAvailablePort(dbType, databases)

	// Save configuration
	if err := m.config.AddDatabase(name, dbType, version, port); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("failed to save database config: %w", err)
	}

	// Create volume
	volumeName := fmt.Sprintf("alios_%s-%s", name, version)
	if err := m.docker.CreateVolume(volumeName); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("failed to create volume: %w", err)
	}

	// Display name for output
	displayName := "MySQL"
	if dbType == "postgresql" {
		displayName = "PostgreSQL"
	}

	fmt.Printf(utils.Colorize("[SUCCESS]", utils.ColorGreen)+" %s '%s' (version %s) configured on port %s\n", displayName, name, version, port)
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
		container := "alios_" + name

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
