package ssl

import (
	"fmt"
	"github.com/aliasproject/alios-cli/internal/utils"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/docker"
)

// Manager handles SSL certificate operations
type Manager struct {
	docker *docker.Client
	config *config.Config
}

// NewManager creates a new SSL manager
func NewManager(dockerClient *docker.Client, cfg *config.Config) *Manager {
	return &Manager{
		docker: dockerClient,
		config: cfg,
	}
}

// SetupSSL sets up the SSL infrastructure
func (m *Manager) SetupSSL() error {
	fmt.Println("🔒 Setting up SSL certificates...")

	// Create certificates volume
	if err := m.docker.CreateVolume("alios_certs"); err != nil {
		return fmt.Errorf("failed to create certificates volume: %w", err)
	}

	// Check if CA certificate already exists
	if !m.caExistsInVolume() {
		fmt.Println(utils.Colorize("[AUTH]", utils.ColorBlue) + " Generating CA certificate...")
		if err := m.generateCA(); err != nil {
			return fmt.Errorf("failed to generate CA certificate: %w", err)
		}
	}

	// CA certificate is now handled via temporary files during installation
	// No need to copy to host directory

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " SSL setup complete")
	return nil
}

// SetupProjectSSL generates SSL certificate for a project
func (m *Manager) SetupProjectSSL(projectName string) error {
	domain := projectName + ".test"

	fmt.Printf("  Generating SSL certificate for %s...\n", domain)

	// Check if CA certificate exists first
	if !m.caExistsInVolume() {
		return fmt.Errorf("CA certificate not found. Run 'alios cert install' first")
	}

	// Check if Caddy is running
	if !m.docker.ContainerExists("alios_caddy") {
		return fmt.Errorf("Caddy container not found. Run 'alios global start' first")
	}

	// Check Caddy container is actually running
	info, err := m.docker.InspectContainer("alios_caddy")
	if err != nil {
		return fmt.Errorf("failed to inspect Caddy container: %w", err)
	}
	if info.State != "running" {
		return fmt.Errorf("Caddy container is not running. Run 'alios global start' first")
	}

	// Generate certificate for domain
	if err := m.GenerateCertificate(domain); err != nil {
		return fmt.Errorf("failed to generate certificate for %s: %w", domain, err)
	}

	// Verify certificate was created
	if !m.certificateExists(domain) {
		return fmt.Errorf("certificate generation failed - files not found for %s", domain)
	}

	// Create Caddy vhost configuration
	if err := m.createCaddyVhost(projectName, domain); err != nil {
		return fmt.Errorf("failed to create Caddy vhost: %w", err)
	}

	// Reload Caddy
	if err := m.reloadCaddy(); err != nil {
		return fmt.Errorf("failed to reload Caddy: %w", err)
	}

	fmt.Printf("  [SUCCESS] SSL certificate configured for %s\n", domain)
	return nil
}

// RepairVhosts regenerates the certificate (if missing) and Caddy vhost
// configuration for each of the given project names, without starting or
// otherwise touching their project containers, and without requiring Caddy to
// currently be running. Useful for applying a vhost template change to
// already-configured projects, including ones that are deliberately stopped.
// It does not reload/restart Caddy itself - the caller does that once, after
// every vhost has been rewritten. Returns the number of projects repaired
// successfully; per-project failures are collected but do not stop the rest.
func (m *Manager) RepairVhosts(projectNames []string) (int, error) {
	if !m.caExistsInVolume() {
		return 0, fmt.Errorf("CA certificate not found. Run 'alios cert install' first")
	}

	var errs []string
	repaired := 0

	for _, name := range projectNames {
		domain := name + ".test"

		if !m.certificateExists(domain) {
			if err := m.GenerateCertificate(domain); err != nil {
				errs = append(errs, fmt.Sprintf("%s: failed to generate certificate: %v", name, err))
				continue
			}
		} else if err := m.fixCertPermissions(domain); err != nil {
			// Heals certs generated before fixCertPermissions existed, or
			// whose permissions drifted some other way - repair should fix
			// exactly this kind of thing, not just missing files.
			errs = append(errs, fmt.Sprintf("%s: failed to fix certificate permissions: %v", name, err))
			continue
		}

		if err := m.createCaddyVhost(name, domain); err != nil {
			errs = append(errs, fmt.Sprintf("%s: failed to write vhost: %v", name, err))
			continue
		}

		repaired++
	}

	if len(errs) > 0 {
		return repaired, fmt.Errorf("failed to repair %d project(s):\n  %s", len(errs), strings.Join(errs, "\n  "))
	}

	return repaired, nil
}

// RestartCaddy restarts the Caddy container so it picks up on-disk vhost
// changes even if it's currently crash-looping (unlike reloadCaddy, which
// requires Caddy to already be in a running state to accept `docker exec`).
func (m *Manager) RestartCaddy() error {
	return m.docker.Run("restart", "alios_caddy")
}

// RemoveProjectSSL removes SSL configuration for a project
func (m *Manager) RemoveProjectSSL(projectName string) error {
	domain := projectName + ".test"

	// Remove certificate files directly from alios_certs volume. Includes
	// .csr alongside .crt/.key: the openssl image only (re)generates each of
	// key/csr/crt when that specific file is missing, so leaving a stale
	// .csr behind means a future generation reuses it as-is - signing it
	// produces a .crt that doesn't match the freshly generated .key.
	// This works regardless of Caddy container state
	if err := m.docker.Run("run", "--rm", "-v", "alios_certs:/certs", "alpine:latest",
		"sh", "-c", fmt.Sprintf("rm -f /certs/%s.crt /certs/%s.key /certs/%s.csr", domain, domain, domain)); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to remove certificate files: %v\n", err)
	}

	// Update vhost to use HTTP instead of HTTPS
	if err := m.updateVhostToHTTP(projectName); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to update vhost to HTTP: %v\n", err)
	}

	return nil
}

// RemoveProjectVhost removes the Caddy vhost configuration for a project
func (m *Manager) RemoveProjectVhost(projectName string) error {
	// Remove vhost file directly from alios_vhosts volume
	// This works regardless of Caddy container state
	if err := m.docker.Run("run", "--rm", "-v", "alios_vhosts:/vhosts", "alpine:latest",
		"rm", "-f", fmt.Sprintf("/vhosts/%s.caddyfile", projectName)); err != nil {
		fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to remove vhost configuration: %v\n", err)
	}

	// Reload Caddy to apply configuration changes if it's running
	if m.docker.ContainerExists("alios_caddy") && m.docker.IsContainerRunning("alios_caddy") {
		if err := m.reloadCaddy(); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to reload Caddy: %v\n", err)
		}
	}

	return nil
}

// updateVhostToHTTP updates a project's vhost configuration to use HTTP instead of HTTPS
func (m *Manager) updateVhostToHTTP(projectName string) error {
	// Create HTTP-only vhost configuration - no tls directive, so Caddy serves
	// this site block in plaintext instead of attempting automatic HTTPS (which
	// can't work for .test domains anyway; see createCaddyVhost).
	httpVhostConfig := fmt.Sprintf(`http://%s.test {
    reverse_proxy alios_php_%s:80 {
        header_up Host {host}
        header_up X-Real-IP {remote}
        header_up X-Forwarded-For {remote}
        header_up X-Forwarded-Proto {scheme}
    }
    handle_errors {
        respond "Project unavailable" 503
    }
}
`, projectName, projectName)

	// Write HTTP vhost configuration to volume
	if err := m.docker.WriteFileToVolume("alios_vhosts", projectName+".caddyfile", []byte(httpVhostConfig)); err != nil {
		return fmt.Errorf("failed to write HTTP vhost configuration: %w", err)
	}

	// Reload Caddy to apply configuration changes if it's running
	if m.docker.ContainerExists("alios_caddy") && m.docker.IsContainerRunning("alios_caddy") {
		if err := m.reloadCaddy(); err != nil {
			fmt.Printf(utils.Colorize("[WARNING]", utils.ColorYellow)+" Warning: Failed to reload Caddy: %v\n", err)
		}
	}

	return nil
}

// GenerateCertificate generates a certificate for a domain
func (m *Manager) GenerateCertificate(domain string) error {
	// Run openssl container to generate certificate with output
	cmd := exec.Command("docker", "run", "--rm",
		"-e", "DOMAIN="+domain,
		"-v", "alios_certs:/certificates",
		"ghcr.io/aliasproject/openssl:latest")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certificate generation failed: %w\nOutput: %s", err, string(output))
	}

	if err := m.fixCertPermissions(domain); err != nil {
		return fmt.Errorf("failed to fix certificate permissions: %w", err)
	}

	return nil
}

// fixCertPermissions makes domain.key group-readable by group 0 (root),
// matching the group Caddy's non-root "appuser" runs as (see docker-caddy's
// Dockerfile: USER ${APP_UID}:0). The openssl image runs as root and writes
// the key 0600 root:root, which Caddy's container otherwise can't read at
// all when loading the tls directive.
func (m *Manager) fixCertPermissions(domain string) error {
	return m.docker.Run("run", "--rm",
		"-v", "alios_certs:/certificates",
		"alpine:latest",
		"sh", "-c", fmt.Sprintf("chgrp 0 /certificates/%s.key && chmod 640 /certificates/%s.key", domain, domain),
	)
}

// InstallCertificate installs the CA certificate to the system
func (m *Manager) InstallCertificate() error {
	// Create temporary file for CA certificate
	tmpFile, err := os.CreateTemp("", "alios-ca-*.pem")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath) // Clean up temp file

	// Copy CA certificate from Docker volume to temp file
	if err := m.copyCAToTempFile(tmpPath); err != nil {
		return fmt.Errorf("failed to copy CA certificate: %w", err)
	}

	fmt.Println(utils.Colorize("[AUTH]", utils.ColorBlue) + " Installing CA certificate to system...")

	switch runtime.GOOS {
	case "darwin":
		if err := m.installCertMacOS(tmpPath); err != nil {
			return err
		}
	case "linux":
		if err := m.installCertLinux(tmpPath); err != nil {
			return err
		}
	case "windows":
		if err := m.installCertWindows(tmpPath); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Certificate installed successfully")
	fmt.Println(utils.Colorize("[RESTARTING]", utils.ColorCyan) + " Please restart your browser for the changes to take effect")
	return nil
}

// VerifyCertificate checks if the CA certificate is installed
func (m *Manager) VerifyCertificate() error {
	installed := m.isCertificateInstalled()

	if installed {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " CA certificate is installed in the system")
	} else {
		fmt.Println(utils.Colorize("[ERROR]", utils.ColorRed) + " CA certificate is not installed")
		fmt.Println("Run 'alios cert install' to install it")
	}

	// Check if CA certificate exists in Docker volume
	if m.caExistsInVolume() {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " CA certificate exists in Docker volume")
	} else {
		fmt.Println(utils.Colorize("[ERROR]", utils.ColorRed) + " CA certificate not found in Docker volume")
		fmt.Println("Run 'alios global start' to generate it")
	}

	return nil
}

// DebugCertificate provides detailed certificate debugging information
func (m *Manager) DebugCertificate() error {
	fmt.Println("🔍 Certificate Debug Information")
	fmt.Println(strings.Repeat("-", 50))

	// Check CA certificate in Docker volume
	fmt.Println("CA Certificate:")
	if m.caExistsInVolume() {
		fmt.Println("  [SUCCESS] Exists in Docker volume")
	} else {
		fmt.Println("  [ERROR] Not found in Docker volume")
	}

	// Check system installation
	fmt.Printf("\nSystem Installation: ")
	if m.isCertificateInstalled() {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Installed")
		m.showSystemCertificateInfo()
	} else {
		fmt.Println(utils.Colorize("[ERROR]", utils.ColorRed) + " Not installed")
	}

	// Check Docker volume
	fmt.Println("\nDocker Volume:")
	if m.caExistsInVolume() {
		fmt.Println("  [SUCCESS] CA exists in volume")
	} else {
		fmt.Println("  [ERROR] CA not found in volume")
	}

	// Check Caddy container
	fmt.Println("\nCaddy Container:")
	if m.docker.IsContainerRunning("alios_caddy") {
		fmt.Println("  [SUCCESS] Running")

		// List certificates in Caddy
		output, err := m.docker.RunWithOutput("exec", "alios_caddy", "ls", "-la", "/etc/caddy/ssl/")
		if err == nil {
			fmt.Println("  Certificates:")
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				if strings.Contains(line, ".crt") || strings.Contains(line, ".key") {
					fmt.Printf("    %s\n", line)
				}
			}
		}
	} else {
		fmt.Println("  [ERROR] Not running")
	}

	return nil
}

// CAExists reports whether the CA certificate has already been generated
// (i.e. SetupSSL has completed at least once). Used to tell a fully-finished
// installation apart from one that stopped partway through.
func (m *Manager) CAExists() bool {
	return m.caExistsInVolume()
}

// Private helper methods

// caExistsInVolume checks if CA certificate exists in volume
func (m *Manager) caExistsInVolume() bool {
	err := m.docker.Run("run", "--rm",
		"-v", "alios_certs:/certificates",
		"alpine:latest",
		"test", "-f", "/certificates/AliasProjectCASelfSigned.pem",
	)
	return err == nil
}

// certificateExists checks if a domain certificate exists in volume
func (m *Manager) certificateExists(domain string) bool {
	// Check for both certificate and key
	certCheck := m.docker.Run("run", "--rm",
		"-v", "alios_certs:/certificates",
		"alpine:latest",
		"test", "-f", fmt.Sprintf("/certificates/%s.crt", domain),
	)

	keyCheck := m.docker.Run("run", "--rm",
		"-v", "alios_certs:/certificates",
		"alpine:latest",
		"test", "-f", fmt.Sprintf("/certificates/%s.key", domain),
	)

	return certCheck == nil && keyCheck == nil
}

func (m *Manager) generateCA() error {
	return m.docker.Run("run", "--rm",
		"-e", "DOMAIN=init",
		"-v", "alios_certs:/certificates",
		"ghcr.io/aliasproject/openssl:latest",
	)
}

// copyCAToTempFile copies the CA certificate from Docker volume to a temporary file
func (m *Manager) copyCAToTempFile(tmpPath string) error {
	// Copy CA from volume to temporary file
	return m.docker.Run("run", "--rm",
		"-v", "alios_certs:/source",
		"-v", filepath.Dir(tmpPath)+":/dest",
		"alpine:latest",
		"cp", "/source/AliasProjectCASelfSigned.pem", "/dest/"+filepath.Base(tmpPath),
	)
}

// createCaddyVhost writes the per-project Caddy site block for domain, using
// an explicit tls directive pointing at the self-signed cert this manager
// already generates via the openssl image. .test domains are IANA-reserved
// (RFC 2606) and never publicly resolvable, so Caddy's automatic HTTPS/ACME
// has no way to obtain a certificate for them - unlike production, which
// relies on Let's Encrypt against real hostnames. Caddy resolves the target
// container by name on the shared Docker network natively, and reverse_proxy
// handles WebSocket upgrade automatically, so neither needs the manual
// resolver/upgrade-header wiring the old nginx vhost required.
func (m *Manager) createCaddyVhost(projectName, domain string) error {
	vhostConfig := fmt.Sprintf(`%s {
    tls /etc/caddy/ssl/%s.crt /etc/caddy/ssl/%s.key

    reverse_proxy alios_php_%s:80 {
        header_up Host {host}
        header_up X-Real-IP {remote}
        header_up X-Forwarded-For {remote}
        header_up X-Forwarded-Proto {scheme}
    }

    handle_errors {
        respond "Project unavailable" 503
    }
}
`, domain, domain, domain, projectName)

	// Write directly to the alios_vhosts volume rather than docker exec-ing into
	// the Caddy container, so vhosts can be written/repaired even when Caddy isn't
	// currently running (e.g. it's down because a stale vhost is crash-looping it).
	// The volume is mounted at /etc/caddy in the Caddy container (see
	// services.Manager.generateComposeConfig), alongside the base Caddyfile that
	// imports every *.caddyfile written here.
	return m.docker.WriteFileToVolume("alios_vhosts", projectName+".caddyfile", []byte(vhostConfig))
}

func (m *Manager) reloadCaddy() error {
	return m.docker.Run("exec", "alios_caddy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
}

func (m *Manager) installCertMacOS(caPath string) error {
	// Remove old certificate if exists
	exec.Command("sudo", "security", "delete-certificate", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain").Run()

	// Add new certificate
	cmd := exec.Command("sudo", "security", "add-trusted-cert",
		"-d",              // Add to admin trust store
		"-r", "trustRoot", // Trust root certificate
		"-p", "ssl", // Trust for SSL
		"-p", "basic", // Trust for basic X.509
		"-k", "/Library/Keychains/System.keychain",
		caPath,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install certificate: %w", err)
	}

	return nil
}

func (m *Manager) installCertLinux(caPath string) error {
	fmt.Println("Installing certificate on Linux...")
	fmt.Println(caPath)

	// Check if 'trust' is available
	_, err := exec.LookPath("trust")
	if err == nil {
		return exec.Command("sudo", "trust", "anchor", caPath).Run()
	}

	// Copy to system certificate store
	destPath := "/usr/local/share/ca-certificates/alios-ca.crt"

	cmd := exec.Command("sudo", "cp", caPath, destPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to copy certificate: %w", err)
	}

	// Update certificate store
	cmd = exec.Command("sudo", "update-ca-certificates")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to update certificates: %w", err)
	}

	return nil
}

func (m *Manager) installCertWindows(caPath string) error {
	// Use PowerShell to import certificate
	script := fmt.Sprintf(`Import-Certificate -FilePath "%s" -CertStoreLocation "Cert:\LocalMachine\Root"`, caPath)

	cmd := exec.Command("powershell", "-Command", script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to install certificate: %w", err)
	}

	return nil
}

func (m *Manager) isCertificateInstalled() bool {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("security", "find-certificate", "-c", "AliasProjectCASelfSigned", "/Library/Keychains/System.keychain")
		return cmd.Run() == nil
	case "linux":
		// installCertLinux uses `trust anchor` when available (Arch, Fedora,
		// and most modern distros) and only falls back to the Debian-style
		// update-ca-certificates file when it isn't - check whichever path
		// was actually used, or this reports "not installed" right after a
		// successful trust-anchor install.
		if _, err := exec.LookPath("trust"); err == nil {
			return m.isCertificateInstalledViaTrust()
		}
		return fileExists("/usr/local/share/ca-certificates/alios-ca.crt")
	case "windows":
		cmd := exec.Command("powershell", "-Command",
			"Get-ChildItem -Path Cert:\\LocalMachine\\Root | Where-Object { $_.Subject -like '*AliasProjectCASelfSigned*' }")
		return cmd.Run() == nil
	default:
		return false
	}
}

// isCertificateInstalledViaTrust checks the p11-kit trust store for the CA
// anchor installCertLinux adds via `trust anchor` (no sudo needed to list,
// only to add/remove). p11-kit's own pretty-printer splits the cert's CN
// into space-separated words for display, so match with whitespace stripped
// rather than an exact substring.
func (m *Manager) isCertificateInstalledViaTrust() bool {
	output, err := exec.Command("trust", "list", "--filter=ca-anchors").CombinedOutput()
	if err != nil {
		return false
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(string(output)), ""))
	return strings.Contains(normalized, "aliasprojectcaselfsigned")
}

func (m *Manager) showSystemCertificateInfo() {
	switch runtime.GOOS {
	case "darwin":
		cmd := exec.Command("security", "find-certificate", "-c", "AliasProjectCASelfSigned", "-p", "/Library/Keychains/System.keychain")
		if output, err := cmd.Output(); err == nil && len(output) > 0 {
			fmt.Println("  Location: System Keychain")
			fmt.Println("  Trust: SSL, Basic X.509")
		}
	case "linux":
		if _, err := exec.LookPath("trust"); err == nil && m.isCertificateInstalledViaTrust() {
			fmt.Println("  Location: p11-kit trust store (trust anchor)")
		} else if fileExists("/usr/local/share/ca-certificates/alios-ca.crt") {
			fmt.Println("  Location: /usr/local/share/ca-certificates/")
		}
	case "windows":
		fmt.Println("  Location: Local Machine\\Root Certificate Store")
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
