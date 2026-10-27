package services

import (
	"fmt"
	"github.com/aliasproject/ingot/internal/utils"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/aliasproject/ingot/internal/config"
	"github.com/aliasproject/ingot/internal/docker"
	"github.com/aliasproject/ingot/internal/ssl"
)

// Manager handles global services operations
type Manager struct {
	docker *docker.Client
	config *config.Config
	ssl    *ssl.Manager
	mu     sync.RWMutex
}

// NewManager creates a new services manager. The SSL manager is needed
// because the reverse proxy's base Caddyfile loads the ingot CA for its
// catch-all site's on-demand certs, so the CA has to exist and be readable
// before Caddy can start.
func NewManager(dockerClient *docker.Client, sslManager *ssl.Manager) *Manager {
	return &Manager{
		docker: dockerClient,
		config: config.Get(),
		ssl:    sslManager,
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

// baseCaddyfile is the reverse proxy's root config. Per-project vhosts are
// imported from the ingot_vhosts volume; anything that doesn't match one of
// them falls through to the host-less catch-all sites, which serve the
// branded default page with a 404 instead of a bare TLS or connection error.
// Caddy always routes host-matched sites ahead of host-less ones, so the
// catch-all can't shadow a project.
//
// TLS for unknown hosts: a static cert can't cover them - browsers (and
// curl) reject a wildcard directly under a TLD like *.test - so the https://
// catch-all uses on-demand issuance from Caddy's internal CA, with that CA
// rooted at the same ingot CA that signs every project cert (the pki block;
// see ssl.Manager.PrepareCAForCaddy for the key permissions). Caddy mints an
// exact cert for whatever host was requested at handshake time, chained to
// a root the browser already trusts, so the 404 page loads with no warning.
// Project hosts never hit this path: their manually loaded certs match
// first. Caddy warns that on-demand has "no protections" (no ask endpoint);
// with an internal issuer on a loopback-only dev proxy there's nothing to
// abuse, so that's left as-is.
//
// The http:// catch-all redirects to HTTPS rather than serving the page:
// declaring an explicit :80 server stops Caddy from adding its own
// HTTP->HTTPS redirects for the manually-certified project vhosts, so this
// redirect covers both those and unknown hosts. An HTTP-only vhost
// (ssl.Manager.updateVhostToHTTP) is host-matched and still wins over it.
const baseCaddyfile = `{
	skip_install_trust
	pki {
		ca ingot {
			name "` + ssl.CAName + `"
			root {
				cert /etc/caddy/ssl/` + ssl.CACertFile + `
				key /etc/caddy/ssl/` + ssl.CAKeyFile + `
			}
		}
	}
}

# Project vhosts end with "import ingot_wake <project>". When the project's
# container is stopped, Caddy can't reach it and the request goes to
# ingot_idle instead, which starts the container and redirects the browser
# back to the same URL. Scoped to upstream failures so the app's own error
# pages, and a share's basic-auth 401, pass through untouched. Defined before
# the import below, since Caddy needs a snippet declared before it's used.
#
# The failed first hop has already consumed the request body, so the waker
# is always asked with a bodiless GET; forwarding a POST would fail on the
# closed body. The browser resends the original method and body itself,
# because the waker answers with a 307.
(ingot_wake) {
	handle_errors 502 503 504 {
		request_header -Content-Length
		request_header -Content-Type
		request_header -Transfer-Encoding
		reverse_proxy ` + IdleContainer + `:8080 {
			method GET
			header_up X-Ingot-Project {args[0]}
		}
	}
}

# "ingot stop" swaps a vhost's ingot_wake import for this one: a project
# stopped by hand stays down until "ingot start", rather than being started
# by the next visit (a polling tab would otherwise undo the stop at once).
(ingot_stopped) {
	handle_errors 502 503 504 {
		header Content-Type "text/plain; charset=utf-8"
		header Cache-Control no-store
		respond "{args[0]} is stopped. Run 'ingot start' in the project to start it again." 503
	}
}

import /etc/caddy/*.caddyfile

https:// {
	tls {
		issuer internal {
			ca ingot
		}
		on_demand
	}
	root * /etc/caddy/default
	rewrite * /index.html
	file_server {
		status 404
	}
}

http:// {
	redir https://{host}{uri} 308
}
`

// GlobalServices defines the core global services. ingot_idle isn't one of
// them: it's optional (see idle.go), so a missing image never takes down
// DNS, Caddy and every project with it.
var GlobalServices = []string{
	"ingot_dns",
	"ingot_proxy",
	"ingot_mail",
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

	// The base Caddyfile's catch-all site (see baseCaddyfile) issues certs
	// for unknown hosts from the ingot CA, which Caddy loads at startup - so
	// the CA has to exist and be readable before the container does. This
	// also creates the CA on a fresh install, so ordering against
	// ssl.SetupSSL doesn't matter.
	if err := m.ssl.PrepareCAForCaddy(); err != nil {
		return fmt.Errorf("failed to prepare CA for the reverse proxy: %w", err)
	}

	if err := m.WriteBaseConfig(); err != nil {
		return err
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

	// Caddy only reads /etc/caddy/Caddyfile at startup. If compose judged the
	// container unchanged and left it running (same image, same mounts), the
	// base Caddyfile just rewritten above is not yet live - e.g. right after
	// an ingot update that changed it - so reload explicitly. A no-op when
	// the container was freshly created or the config is unchanged.
	if err := m.ssl.ReloadCaddy(); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to reload Caddy config: %v\n", err)
	}

	if err := m.startIdle(); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Scale to zero is unavailable, so idle projects won't be stopped or woken: %v\n", err)
	}

	return nil
}

// WriteBaseConfig writes the base Caddyfile and the default page into the
// vhosts volume. The published caddy image's own baked-in Caddyfile only
// serves a landing page and has no knowledge of per-project vhosts; mounting
// ingot_vhosts at /etc/caddy replaces that entirely, so this file must exist
// in the volume (even empty of any project) or Caddy has nothing to run.
// Rewritten on every start, and before `cert repair` rewrites vhosts, so it
// can't drift from what those vhosts expect (the ingot_wake snippet).
func (m *Manager) WriteBaseConfig() error {
	if err := m.docker.WriteFileToVolume("ingot_vhosts", "default/index.html", defaultPage); err != nil {
		return fmt.Errorf("failed to write default page: %w", err)
	}
	if err := m.docker.WriteFileToVolume("ingot_vhosts", "Caddyfile", []byte(baseCaddyfile)); err != nil {
		return fmt.Errorf("failed to write base Caddyfile: %w", err)
	}
	return nil
}

// Stop stops every global-service container (core services and any database)
// without removing them. Containers are the only place a database's
// existence is recorded (config.Config.Databases is in-memory only and empty
// on every fresh CLI invocation - see config.go), so a `compose down` that
// actually removed orphans would silently lose every database on the next
// Start. A plain `compose down` with an empty services list is a no-op on
// modern Compose, which is what this used to do; stopping by name is what
// "stop" means and keeps the database containers around for the next Start.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, err := m.Pause()
	return err
}

// Restart stops all global services and brings them back up through the
// compose-based Start, which recreates any core container whose definition
// changed (new image, new mounts) and starts the rest as-is. Database
// containers aren't in the fresh compose config (see Stop), so they're
// started back up by name afterwards.
func (m *Manager) Restart() error {
	m.mu.Lock()
	stopped, err := m.Pause()
	m.mu.Unlock()
	if err != nil {
		return err
	}

	// Wait a moment for clean shutdown
	time.Sleep(2 * time.Second)

	if err := m.Start(); err != nil {
		return err
	}

	for _, name := range stopped {
		if m.docker.IsContainerRunning(name) {
			continue
		}
		if err := m.docker.StartContainer(name); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to start '%s': %v\n", name, err)
		}
	}

	return nil
}

// Status returns the status of all global services
func (m *Manager) Status() ([]*Service, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	services := make([]*Service, 0)

	// Check core services, plus the optional ingot_idle so a stopped or
	// crashed one shows up in status rather than silently disabling scale
	// to zero.
	for _, containerName := range append(GlobalServices[:len(GlobalServices):len(GlobalServices)], IdleContainer) {
		displayName := strings.TrimPrefix(containerName, "ingot_")
		if displayName == "proxy" {
			displayName = "caddy"
		}
		service := &Service{
			Name:      displayName,
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
		dbContainer := "ingot_" + name
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

// ListManagedContainers enumerates every global-service container (dns,
// the reverse proxy, mail, and any database): GlobalServices members by
// exact name, everything else (databases) by the "ingot_" name prefix,
// excluding project containers ("ingot_php_*"). Discovery is done by name
// rather than by label or in-process config because pause/resume run as
// separate CLI invocations that don't share config.Config.Databases with
// each other.
func (m *Manager) ListManagedContainers() ([]string, error) {
	names, err := m.docker.ListContainers("")
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Global services all share the ingot_ prefix today; checking
	// GlobalServices membership as well keeps one that ever doesn't
	// tracked for pause/resume.
	globalServiceNames := make(map[string]bool, len(GlobalServices))
	for _, n := range GlobalServices {
		globalServiceNames[n] = true
	}

	managed := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		// Share tunnels (ingot_share, ingot_share_<project>) aren't global
		// services: restarting a quick tunnel gives it a new public URL, which
		// would leave its vhost pointing at a host nothing routes to. They're
		// ended by `ingot pause` and `ingot share stop` instead.
		if strings.HasPrefix(name, "ingot_share") {
			continue
		}
		if globalServiceNames[name] || (strings.HasPrefix(name, "ingot_") && !strings.HasPrefix(name, "ingot_php_")) {
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

	compose := fmt.Sprintf(`name: ingot
services:
  dns:
    container_name: ingot_dns
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
    container_name: ingot_proxy
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

  # Mailpit, not MailHog: MailHog only ships amd64 images, which can't
  # run on arm64 Linux hosts without QEMU. Same ports (1025 SMTP, 8025 UI).
  # Accept any SMTP credentials so apps configured with a username and
  # password for production mail don't get rejected locally.
  mail:
    container_name: ingot_mail
    image: axllent/mailpit:v1.32
    restart: on-failure
    environment:
      - MP_SMTP_AUTH_ACCEPT_ANY=1
      - MP_SMTP_AUTH_ALLOW_INSECURE=1
    ports:
      - "1025:1025"
      - "8025:8025"
    networks:
      - internal
`, hostIP)

	// Add databases
	for name, dbConfig := range databases {
		if dbConfig.Type == "mysql" {
			// mysql:5.7 only ships amd64 images, so pin it and let Docker
			// Desktop emulate it on Apple Silicon. 8.x is multi-arch and
			// runs natively, including on arm64 Linux hosts without QEMU.
			platform := ""
			if dbConfig.Version == "5.7" {
				platform = "\n    platform: linux/amd64"
			}
			compose += fmt.Sprintf(`
  %s:
    container_name: ingot_%s
    image: mysql:%s%s
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
`, name, name, dbConfig.Version, platform, dbConfig.Port, name, dbConfig.Version)
		} else if dbConfig.Type == "postgresql" {
			compose += fmt.Sprintf(`
  %s:
    container_name: ingot_%s
    image: postgres:%s
    restart: unless-stopped
    environment:
      - POSTGRES_USER=ingot
      - POSTGRES_PASSWORD=ingot
      - POSTGRES_DB=ingot
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
    name: ingot_certs
  vhosts:
    name: ingot_vhosts`

	// Add volumes for each database
	for name, dbConfig := range databases {
		compose += fmt.Sprintf(`
  %s-%s:
    name: ingot_%s-%s`, name, dbConfig.Version, name, dbConfig.Version)
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
	volumeName := fmt.Sprintf("ingot_%s-%s", name, version)
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
		container := "ingot_" + name

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
