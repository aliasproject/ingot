// Package share exposes running projects to the internet through Cloudflare
// Tunnels (alios share).
//
// Traffic path: cloudflared (a container on alios_internal) -> Caddy
// (http://alios-reverse-proxy:80) -> alios_php_<project>:80. Each share is one
// <project>.share.caddyfile vhost in the alios_vhosts volume, matched on the
// exact public hostname so it wins over the base Caddyfile's http:// redirect
// catch-all. That vhost file is the only record of a share: status, list and
// stop all read it back, so there's no other state to keep in sync.
//
// Two modes:
//   - quick (default): one alios_share_<project> container running a
//     trycloudflare.com quick tunnel. No account needed; the URL is random and
//     changes every time the tunnel starts.
//   - named (ALIOS_SHARE_TUNNEL_TOKEN + ALIOS_SHARE_DOMAIN): one shared
//     alios_share container running a remotely-managed tunnel whose
//     `*.<domain>` public hostname points at Caddy, giving each project a
//     stable https://<project>.<domain> URL.
package share

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/docker"
	"github.com/aliasproject/alios-cli/internal/ssl"
)

const (
	// Image is the cloudflared image tunnels run from. Pinned so a tunnel
	// behaves the same on every machine; bump deliberately.
	Image = "cloudflare/cloudflared:2026.9.3"

	// NamedContainer is the single shared container used in named mode.
	NamedContainer = "alios_share"

	// ModeQuick and ModeNamed identify how a share is tunnelled.
	ModeQuick = "quick"
	ModeNamed = "named"

	vhostVolume     = "alios_vhosts"
	vhostSuffix     = ".share.caddyfile"
	proxyContainer  = "alios-reverse-proxy"
	proxyOrigin     = "http://" + proxyContainer + ":80"
	projectLabel    = "alios.share.project"
	modeLabel       = "alios.share.mode"
	quickURLTimeout = 30 * time.Second
)

var quickURLPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Credentials protect a share with HTTP basic auth.
type Credentials struct {
	User     string
	Password string
}

// ParseCredentials parses a "user:pass" --auth value.
func ParseCredentials(value string) (*Credentials, error) {
	user, pass, ok := strings.Cut(value, ":")
	if !ok || user == "" || pass == "" {
		return nil, fmt.Errorf("invalid --auth value, expected user:password")
	}
	if strings.ContainsAny(user, " \t{}\"") {
		return nil, fmt.Errorf("invalid --auth username %q: no spaces, braces or quotes", user)
	}
	return &Credentials{User: user, Password: pass}, nil
}

// Share describes one active share.
type Share struct {
	Project string
	URL     string
	Mode    string
	Auth    bool
}

// Manager starts and stops project shares.
type Manager struct {
	docker *docker.Client
	config *config.Config
	ssl    *ssl.Manager
}

// NewManager creates a share manager.
func NewManager(dockerClient *docker.Client, cfg *config.Config, sslManager *ssl.Manager) *Manager {
	return &Manager{docker: dockerClient, config: cfg, ssl: sslManager}
}

// QuickContainer is the tunnel container name for a quick-mode share.
func QuickContainer(projectName string) string {
	return "alios_share_" + projectName
}

// Get returns the project's active share, or nil if it isn't shared.
func (m *Manager) Get(projectName string) (*Share, error) {
	content, err := m.docker.ReadFileFromVolume(vhostVolume, projectName+vhostSuffix)
	if err == docker.ErrFileNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseVhost(projectName, content), nil
}

// List returns every active share across all projects, sorted by project.
func (m *Manager) List() ([]*Share, error) {
	files, err := m.docker.ListVolumeFiles(vhostVolume, "*"+vhostSuffix)
	if err != nil {
		return nil, err
	}

	var shares []*Share
	for _, file := range files {
		share, err := m.Get(strings.TrimSuffix(file, vhostSuffix))
		if err != nil {
			return nil, err
		}
		if share != nil {
			shares = append(shares, share)
		}
	}

	sort.Slice(shares, func(i, j int) bool { return shares[i].Project < shares[j].Project })
	return shares, nil
}

// TunnelRunning reports whether the tunnel container serving a share is up.
// A share whose tunnel is gone (e.g. after a reboot) is orphaned: its vhost
// still exists but nothing routes traffic to it.
func (m *Manager) TunnelRunning(share *Share) bool {
	if share.Mode == ModeNamed {
		return m.docker.IsContainerRunning(NamedContainer)
	}
	return m.docker.IsContainerRunning(QuickContainer(share.Project))
}

// Start shares a project and returns the share. If the project is already
// shared with a live tunnel, the existing share is returned and started is
// false; an orphaned share is torn down and replaced.
func (m *Manager) Start(projectName string, auth *Credentials) (share *Share, started bool, err error) {
	existing, err := m.Get(projectName)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read existing share: %w", err)
	}
	if existing != nil {
		if m.TunnelRunning(existing) {
			return existing, false, nil
		}
		if _, err := m.Stop(projectName); err != nil {
			return nil, false, fmt.Errorf("failed to clean up stale share: %w", err)
		}
	}

	if !m.docker.IsContainerRunning(proxyContainer) {
		return nil, false, fmt.Errorf("reverse proxy is not running. Run 'alios global start' first")
	}

	// Hash before starting a tunnel so a bad password never leaves a
	// public tunnel running without its auth.
	var authLine string
	if auth != nil {
		hash, err := m.hashPassword(auth.Password)
		if err != nil {
			return nil, false, err
		}
		authLine = fmt.Sprintf("    basic_auth {\n        %s %s\n    }\n\n", auth.User, hash)
	}

	if err := m.ensureImage(); err != nil {
		return nil, false, err
	}

	var host, mode string
	if m.config.ShareNamedMode() {
		mode = ModeNamed
		host = PublicLabel(projectName) + "." + m.config.ShareDomain
		if err := m.ensureNamedTunnel(); err != nil {
			return nil, false, err
		}
	} else {
		mode = ModeQuick
		host, err = m.startQuickTunnel(projectName)
		if err != nil {
			return nil, false, err
		}
	}

	if err := m.writeVhost(projectName, host, mode, authLine); err != nil {
		m.teardownTunnel(projectName, mode)
		return nil, false, err
	}

	return &Share{Project: projectName, URL: "https://" + host, Mode: mode, Auth: auth != nil}, true, nil
}

// Stop ends a project's share, removing its vhost and tunnel. Returns false if
// the project wasn't shared.
func (m *Manager) Stop(projectName string) (bool, error) {
	share, err := m.Get(projectName)
	if err != nil {
		return false, err
	}

	// A quick tunnel container can outlive its vhost if a previous start
	// failed half-way; always clear it.
	quickExisted := m.docker.ContainerExists(QuickContainer(projectName))
	if share == nil && !quickExisted {
		return false, nil
	}

	if share != nil {
		if err := m.docker.RemoveFileFromVolume(vhostVolume, projectName+vhostSuffix); err != nil {
			return false, fmt.Errorf("failed to remove share vhost: %w", err)
		}
		m.reloadCaddy()
	}

	mode := ModeQuick
	if share != nil {
		mode = share.Mode
	}
	m.teardownTunnel(projectName, mode)
	if mode == ModeNamed {
		m.docker.RemoveContainer(QuickContainer(projectName), true)
	}

	return true, nil
}

// StopAll ends every share and removes every tunnel container, including
// orphans. Returns the projects that were shared.
func (m *Manager) StopAll() ([]string, error) {
	shares, err := m.List()
	if err != nil {
		return nil, err
	}

	var stopped []string
	for _, share := range shares {
		if _, err := m.Stop(share.Project); err != nil {
			return stopped, fmt.Errorf("failed to stop share for %s: %w", share.Project, err)
		}
		stopped = append(stopped, share.Project)
	}

	leftovers, err := m.docker.ListContainers("label=" + projectLabel)
	if err == nil {
		for _, name := range leftovers {
			m.docker.RemoveContainer(name, true)
		}
	}

	return stopped, nil
}

// PublicLabel turns a project name into a single DNS label, so a project named
// e.g. "michaelaguiar.dev" becomes "michaelaguiar-dev" rather than adding a
// subdomain level (which Cloudflare's free wildcard certificate won't cover).
func PublicLabel(projectName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(projectName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func (m *Manager) ensureImage() error {
	if m.docker.ImageExists(Image) {
		return nil
	}
	if err := m.docker.PullImage(Image, nil); err != nil {
		return fmt.Errorf("failed to pull %s: %w", Image, err)
	}
	return nil
}

// startQuickTunnel runs a trycloudflare quick tunnel for the project and
// returns its public hostname, read from cloudflared's startup log.
func (m *Manager) startQuickTunnel(projectName string) (string, error) {
	name := QuickContainer(projectName)
	m.docker.RemoveContainer(name, true)

	err := m.docker.RunContainer(&docker.ContainerConfig{
		Name:     name,
		Image:    Image,
		Detached: true,
		// A restarted quick tunnel gets a new URL, which would silently
		// orphan the vhost - better for it to stay down and show as stale.
		Restart: "no",
		Network: config.GetNetworkConfig().BridgeName,
		Labels: map[string]string{
			projectLabel: projectName,
			modeLabel:    ModeQuick,
		},
		Command: []string{"tunnel", "--no-autoupdate", "--url", proxyOrigin},
	})
	if err != nil {
		return "", fmt.Errorf("failed to start tunnel container: %w", err)
	}

	deadline := time.Now().Add(quickURLTimeout)
	var logs string
	for time.Now().Before(deadline) {
		logs, _ = m.docker.RunWithOutput("logs", name)
		if url := quickURLPattern.FindString(logs); url != "" {
			return strings.TrimPrefix(url, "https://"), nil
		}
		if !m.docker.IsContainerRunning(name) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	m.docker.RemoveContainer(name, true)
	return "", fmt.Errorf("tunnel did not report a public URL within %s. Last output:\n%s", quickURLTimeout, lastLines(logs, 10))
}

// ensureNamedTunnel makes sure the shared named-tunnel container is running.
func (m *Manager) ensureNamedTunnel() error {
	if m.docker.IsContainerRunning(NamedContainer) {
		return nil
	}
	if m.docker.ContainerExists(NamedContainer) {
		if err := m.docker.StartContainer(NamedContainer); err == nil {
			return nil
		}
		m.docker.RemoveContainer(NamedContainer, true)
	}

	err := m.docker.RunContainer(&docker.ContainerConfig{
		Name:     NamedContainer,
		Image:    Image,
		Detached: true,
		Restart:  "unless-stopped",
		Network:  config.GetNetworkConfig().BridgeName,
		// Passed via env rather than argv so it doesn't show in `ps`.
		Environment: map[string]string{"TUNNEL_TOKEN": m.config.ShareTunnelToken},
		Labels: map[string]string{
			projectLabel: "",
			modeLabel:    ModeNamed,
		},
		Command: []string{"tunnel", "--no-autoupdate", "run"},
	})
	if err != nil {
		return fmt.Errorf("failed to start named tunnel container: %w", err)
	}

	// cloudflared exits almost immediately on a bad token; catch that here
	// rather than leaving the user with a URL that never loads.
	time.Sleep(3 * time.Second)
	if !m.docker.IsContainerRunning(NamedContainer) {
		logs, _ := m.docker.RunWithOutput("logs", NamedContainer)
		m.docker.RemoveContainer(NamedContainer, true)
		return fmt.Errorf("named tunnel exited on startup (check ALIOS_SHARE_TUNNEL_TOKEN). Last output:\n%s", lastLines(logs, 10))
	}

	return nil
}

// teardownTunnel removes the tunnel serving a share. In named mode the shared
// container is only removed once no share vhosts remain: left running with no
// matching vhost, requests would hit Caddy's http->https redirect and loop
// back through Cloudflare.
func (m *Manager) teardownTunnel(projectName, mode string) {
	if mode != ModeNamed {
		m.docker.RemoveContainer(QuickContainer(projectName), true)
		return
	}

	remaining, err := m.docker.ListVolumeFiles(vhostVolume, "*"+vhostSuffix)
	if err != nil {
		return
	}
	for _, file := range remaining {
		if share, err := m.Get(strings.TrimSuffix(file, vhostSuffix)); err == nil && share != nil && share.Mode == ModeNamed {
			return
		}
	}
	m.docker.RemoveContainer(NamedContainer, true)
}

func (m *Manager) writeVhost(projectName, host, mode, authLine string) error {
	vhost := fmt.Sprintf(`# alios share: mode=%s
http://%s {
%s    reverse_proxy alios_php_%s:80 {
        header_up Host {host}
        header_up X-Forwarded-Host {host}
        header_up X-Forwarded-Proto https
        header_up X-Real-IP {header.Cf-Connecting-Ip}
        header_up X-Forwarded-For {header.Cf-Connecting-Ip}
        # Tells the image or app that the request came through a share.
        # Set here, so visitors can't forge or strip it.
        header_up X-Alios-Share 1
    }

    # Scoped to upstream failures so basic_auth's 401 (and its
    # WWW-Authenticate challenge) passes through untouched.
    handle_errors 502 503 504 {
        respond "Project unavailable" 503
    }
}
`, mode, host, authLine, projectName)

	if err := m.docker.WriteFileToVolume(vhostVolume, projectName+vhostSuffix, []byte(vhost)); err != nil {
		return fmt.Errorf("failed to write share vhost: %w", err)
	}

	if err := m.ssl.ReloadCaddy(); err != nil {
		m.docker.RemoveFileFromVolume(vhostVolume, projectName+vhostSuffix)
		m.reloadCaddy()
		return fmt.Errorf("failed to reload Caddy: %w", err)
	}

	return nil
}

func (m *Manager) reloadCaddy() {
	if m.docker.IsContainerRunning(proxyContainer) {
		m.ssl.ReloadCaddy()
	}
}

// hashPassword bcrypt-hashes a basic-auth password with Caddy's own tool, so
// alios doesn't need a bcrypt dependency.
func (m *Manager) hashPassword(password string) (string, error) {
	output, err := m.docker.RunWithOutput("exec", proxyContainer, "caddy", "hash-password", "--plaintext", password)
	if err != nil {
		return "", fmt.Errorf("failed to hash --auth password: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	hash := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(hash, "$2") {
		return "", fmt.Errorf("unexpected output hashing --auth password: %s", output)
	}
	return hash, nil
}

// parseVhost reads a share back out of the vhost file writeVhost produced.
func parseVhost(projectName, content string) *Share {
	share := &Share{Project: projectName, Mode: ModeQuick}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "# alios share:") && strings.Contains(line, "mode="+ModeNamed):
			share.Mode = ModeNamed
		case strings.HasPrefix(line, "http://") && share.URL == "":
			host := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "http://"), "{"))
			share.URL = "https://" + host
		case strings.HasPrefix(line, "basic_auth"):
			share.Auth = true
		}
	}
	return share
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
