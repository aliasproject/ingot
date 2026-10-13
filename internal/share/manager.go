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
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	tokenLabel      = "alios.share.token_sha256"
	quickURLTimeout = 30 * time.Second
)

var quickURLPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// quickTunnelURL returns the share URL from cloudflared's log, skipping
// api.trycloudflare.com: cloudflared names that host in its error when it
// can't reach the quick-tunnel service (offline, DNS failure, outage).
func quickTunnelURL(logs string) string {
	for _, url := range quickURLPattern.FindAllString(logs, -1) {
		if url != "https://api.trycloudflare.com" {
			return url
		}
	}
	return ""
}

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
		host, err = m.namedHost(projectName)
		if err != nil {
			return nil, false, err
		}
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
		// Until Caddy reloads it keeps serving the old vhost from memory, so
		// with a named tunnel still up for other shares the project would
		// stay public. Report that rather than claiming the share ended.
		if err := m.reloadCaddy(); err != nil {
			m.teardownTunnel(projectName, share.Mode)
			return false, fmt.Errorf("removed the share vhost but Caddy failed to reload, so it may still be served; run 'alios global restart': %w", err)
		}
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
// orphans. It keeps going past a share that fails to stop, so one bad share
// can't leave the others' tunnels running, and always sweeps the tunnel
// containers - with those gone no share is reachable, whatever else failed.
// Returns the projects whose shares ended, and every error joined.
func (m *Manager) StopAll() ([]string, error) {
	var errs []error
	var stopped []string

	shares, err := m.List()
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list shares: %w", err))
	}
	for _, share := range shares {
		if _, err := m.Stop(share.Project); err != nil {
			errs = append(errs, fmt.Errorf("failed to stop share for %s: %w", share.Project, err))
			continue
		}
		stopped = append(stopped, share.Project)
	}

	leftovers, err := m.docker.ListContainers("label=" + projectLabel)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to list tunnel containers: %w", err))
	}
	for _, name := range leftovers {
		if name == "" {
			continue
		}
		if err := m.docker.RemoveContainer(name, true); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove tunnel container %s: %w", name, err))
		}
	}

	return stopped, errors.Join(errs...)
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

// namedHost returns the public host for a named-mode share, refusing one
// that isn't a valid DNS label or that another project's share already uses
// (PublicLabel maps both my_app and my.app to my-app).
func (m *Manager) namedHost(projectName string) (string, error) {
	label := PublicLabel(projectName)
	if label == "" || len(label) > 63 {
		return "", fmt.Errorf("can't make a public hostname from project name %q (needs 1-63 letters, digits or dashes)", projectName)
	}
	host := label + "." + m.config.ShareDomain

	shares, err := m.List()
	if err != nil {
		return "", fmt.Errorf("failed to read existing shares: %w", err)
	}
	for _, other := range shares {
		if other.Project != projectName && other.URL == "https://"+host {
			return "", fmt.Errorf("https://%s is already used by the share for %s; stop that share first", host, other.Project)
		}
	}
	return host, nil
}

// tokenFingerprint identifies a tunnel token without storing it in a label.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
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
		if url := quickTunnelURL(logs); url != "" {
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
// A container started with a different ALIOS_SHARE_TUNNEL_TOKEN (rotated
// since) is replaced rather than reused.
func (m *Manager) ensureNamedTunnel() error {
	fingerprint := tokenFingerprint(m.config.ShareTunnelToken)
	if info, err := m.docker.InspectContainer(NamedContainer); err == nil {
		if info.Labels[tokenLabel] == fingerprint {
			if info.State == "running" {
				return nil
			}
			if err := m.docker.StartContainer(NamedContainer); err == nil {
				return m.checkNamedTunnelUp()
			}
		}
		m.docker.RemoveContainer(NamedContainer, true)
	}

	err := m.docker.RunContainer(&docker.ContainerConfig{
		Name:     NamedContainer,
		Image:    Image,
		Detached: true,
		Restart:  "unless-stopped",
		Network:  config.GetNetworkConfig().BridgeName,
		// cloudflared reads the token from env. It's still visible to anyone
		// who can run `docker inspect`, as any container env is.
		Environment: map[string]string{"TUNNEL_TOKEN": m.config.ShareTunnelToken},
		Labels: map[string]string{
			projectLabel: "",
			modeLabel:    ModeNamed,
			tokenLabel:   fingerprint,
		},
		Command: []string{"tunnel", "--no-autoupdate", "run"},
	})
	if err != nil {
		return fmt.Errorf("failed to start named tunnel container: %w", err)
	}

	return m.checkNamedTunnelUp()
}

// checkNamedTunnelUp waits out cloudflared's startup: it exits almost
// immediately on a bad token, so catch that here rather than leaving the user
// with a URL that never loads.
func (m *Manager) checkNamedTunnelUp() error {
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

// reloadCaddy applies vhost changes. A stopped Caddy isn't serving anything,
// so there's nothing to reload.
func (m *Manager) reloadCaddy() error {
	if m.docker.IsContainerRunning(proxyContainer) {
		return m.ssl.ReloadCaddy()
	}
	return nil
}

// hashPassword bcrypt-hashes a basic-auth password with Caddy's own tool, so
// alios doesn't need a bcrypt dependency.
func (m *Manager) hashPassword(password string) (string, error) {
	// Sent on stdin rather than --plaintext, so it never shows in `ps`.
	output, err := m.docker.RunWithInput(password+"\n", "exec", "-i", proxyContainer, "caddy", "hash-password")
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
