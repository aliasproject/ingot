package ssl

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/aliasproject/devbox/internal/config"
	"github.com/aliasproject/devbox/internal/docker"
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
	if err := m.docker.CreateVolume("devbox_certs"); err != nil {
		return fmt.Errorf("failed to create certificates volume: %w", err)
	}

	// Check if CA certificate already exists
	if !m.caExistsInVolume() {
		fmt.Println("🔐 Generating CA certificate...")
		if err := m.generateCA(); err != nil {
			return fmt.Errorf("failed to generate CA certificate: %w", err)
		}
	}

	// Copy CA to host
	if err := m.copyCAToHost(); err != nil {
		return fmt.Errorf("failed to copy CA to host: %w", err)
	}

	fmt.Println("✅ SSL setup complete")
	return nil
}

// SetupProjectSSL generates SSL certificate for a project
func (m *Manager) SetupProjectSSL(projectName string) error {
	domain := projectName + ".test"

	fmt.Printf("  Generating SSL certificate for %s...\n", domain)

	// Check if CA certificate exists first
	if !m.caExistsInVolume() {
		return fmt.Errorf("CA certificate not found. Run 'devbox cert install' first")
	}

	// Check if nginx is running
	if !m.docker.ContainerExists("devbox_nginx") {
		return fmt.Errorf("nginx container not found. Run 'devbox global start' first")
	}

	// Check nginx container is actually running
	info, err := m.docker.InspectContainer("devbox_nginx")
	if err != nil {
		return fmt.Errorf("failed to inspect nginx container: %w", err)
	}
	if info.State != "running" {
		return fmt.Errorf("nginx container is not running. Run 'devbox global start' first")
	}

	// Generate certificate for domain
	if err := m.GenerateCertificate(domain); err != nil {
		return fmt.Errorf("failed to generate certificate for %s: %w", domain, err)
	}

	// Verify certificate was created
	if !m.certificateExists(domain) {
		return fmt.Errorf("certificate generation failed - files not found for %s", domain)
	}

	// Create nginx vhost configuration
	if err := m.createNginxVhost(projectName, domain); err != nil {
		return fmt.Errorf("failed to create nginx vhost: %w", err)
	}

	// Reload nginx
	if err := m.reloadNginx(); err != nil {
		return fmt.Errorf("failed to reload nginx: %w", err)
	}

	fmt.Printf("  ✅ SSL certificate configured for %s\n", domain)
	return nil
}

// RemoveProjectSSL removes SSL configuration for a project
func (m *Manager) RemoveProjectSSL(projectName string) error {
	domain := projectName + ".test"

	// Remove certificate files directly from devbox_certs volume
	// This works regardless of nginx container state
	if err := m.docker.Run("run", "--rm", "-v", "devbox_certs:/certs", "alpine:latest",
		"sh", "-c", fmt.Sprintf("rm -f /certs/%s.crt /certs/%s.key", domain, domain)); err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove certificate files: %v\n", err)
	}

	// Update vhost to use HTTP instead of HTTPS
	if err := m.updateVhostToHTTP(projectName); err != nil {
		fmt.Printf("⚠️  Warning: Failed to update vhost to HTTP: %v\n", err)
	}

	return nil
}

// RemoveProjectVhost removes the nginx vhost configuration for a project
func (m *Manager) RemoveProjectVhost(projectName string) error {
	// Remove vhost file directly from devbox_vhosts volume
	// This works regardless of nginx container state
	if err := m.docker.Run("run", "--rm", "-v", "devbox_vhosts:/vhosts", "alpine:latest",
		"rm", "-f", fmt.Sprintf("/vhosts/%s.conf", projectName)); err != nil {
		fmt.Printf("⚠️  Warning: Failed to remove vhost configuration: %v\n", err)
	}

	// Reload nginx to apply configuration changes if it's running
	if m.docker.ContainerExists("devbox_nginx") && m.docker.IsContainerRunning("devbox_nginx") {
		if err := m.reloadNginx(); err != nil {
			fmt.Printf("⚠️  Warning: Failed to reload nginx: %v\n", err)
		}
	}

	return nil
}

// updateVhostToHTTP updates a project's vhost configuration to use HTTP instead of HTTPS
func (m *Manager) updateVhostToHTTP(projectName string) error {
	// Create HTTP-only vhost configuration
	httpVhostConfig := fmt.Sprintf(`server {
    listen 80;
    server_name %s.test;
    root /var/www/%s;
    index index.php index.html index.htm;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        proxy_pass http://%s:80;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}`, projectName, projectName, fmt.Sprintf("devbox_php_%s", projectName))

	// Write HTTP vhost configuration to volume
	if err := m.docker.Run("run", "--rm", "-v", "devbox_vhosts:/vhosts", "alpine:latest",
		"sh", "-c", fmt.Sprintf("echo '%s' > /vhosts/%s.conf", httpVhostConfig, projectName)); err != nil {
		return fmt.Errorf("failed to write HTTP vhost configuration: %w", err)
	}

	// Reload nginx to apply configuration changes if it's running
	if m.docker.ContainerExists("devbox_nginx") && m.docker.IsContainerRunning("devbox_nginx") {
		if err := m.reloadNginx(); err != nil {
			fmt.Printf("⚠️  Warning: Failed to reload nginx: %v\n", err)
		}
	}

	return nil
}

// GenerateCertificate generates a certificate for a domain
func (m *Manager) GenerateCertificate(domain string) error {
	// Run openssl container to generate certificate with output
	cmd := exec.Command("docker", "run", "--rm",
		"-e", "DOMAIN="+domain,
		"-v", "devbox_certs:/certificates",
		"ghcr.io/aliasproject/openssl:latest")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("certificate generation failed: %w\nOutput: %s", err, string(output))
	}

	return nil
}

// InstallCertificate installs the CA certificate to the system
func (m *Manager) InstallCertificate() error {
	caPath := m.config.CAPath

	if !fileExists(caPath) {
		// Try to copy from volume first
		if err := m.copyCAToHost(); err != nil {
			return fmt.Errorf("CA certificate not found at %s", caPath)
		}
	}

	fmt.Println("🔐 Installing CA certificate to system...")

	switch runtime.GOOS {
	case "darwin":
		if err := m.installCertMacOS(caPath); err != nil {
			return err
		}
	case "linux":
		if err := m.installCertLinux(caPath); err != nil {
			return err
		}
	case "windows":
		if err := m.installCertWindows(caPath); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	fmt.Println("✅ Certificate installed successfully")
	fmt.Println("🔄 Please restart your browser for the changes to take effect")
	return nil
}

// VerifyCertificate checks if the CA certificate is installed
func (m *Manager) VerifyCertificate() error {
	installed := m.isCertificateInstalled()

	if installed {
		fmt.Println("✅ CA certificate is installed in the system")
	} else {
		fmt.Println("❌ CA certificate is not installed")
		fmt.Println("Run 'devbox cert install' to install it")
	}

	// Check if certificate file exists
	if fileExists(m.config.CAPath) {
		fmt.Printf("✅ Certificate file exists at: %s\n", m.config.CAPath)
	} else {
		fmt.Printf("❌ Certificate file not found at: %s\n", m.config.CAPath)
	}

	return nil
}

// DebugCertificate provides detailed certificate debugging information
func (m *Manager) DebugCertificate() error {
	fmt.Println("🔍 Certificate Debug Information")
	fmt.Println(strings.Repeat("-", 50))

	// Check CA file
	fmt.Printf("CA Path: %s\n", m.config.CAPath)
	if fileExists(m.config.CAPath) {
		info, _ := os.Stat(m.config.CAPath)
		fmt.Printf("  ✅ Exists (size: %d bytes)\n", info.Size())

		// Show certificate details
		cmd := exec.Command("openssl", "x509", "-in", m.config.CAPath, "-text", "-noout")
		if output, err := cmd.CombinedOutput(); err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, "Subject:") || strings.Contains(line, "Issuer:") ||
					strings.Contains(line, "Not Before") || strings.Contains(line, "Not After") {
					fmt.Printf("  %s\n", strings.TrimSpace(line))
				}
			}
		}
	} else {
		fmt.Println("  ❌ Not found")
	}

	// Check system installation
	fmt.Printf("\nSystem Installation: ")
	if m.isCertificateInstalled() {
		fmt.Println("✅ Installed")
		m.showSystemCertificateInfo()
	} else {
		fmt.Println("❌ Not installed")
	}

	// Check Docker volume
	fmt.Println("\nDocker Volume:")
	if m.caExistsInVolume() {
		fmt.Println("  ✅ CA exists in volume")
	} else {
		fmt.Println("  ❌ CA not found in volume")
	}

	// Check nginx container
	fmt.Println("\nNginx Container:")
	if m.docker.IsContainerRunning("devbox_nginx") {
		fmt.Println("  ✅ Running")

		// List certificates in nginx
		output, err := m.docker.RunWithOutput("exec", "devbox_nginx", "ls", "-la", "/etc/nginx/ssl/")
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
		fmt.Println("  ❌ Not running")
	}

	return nil
}

// Private helper methods

// caExistsInVolume checks if CA certificate exists in volume
func (m *Manager) caExistsInVolume() bool {
	err := m.docker.Run("run", "--rm",
		"-v", "devbox_certs:/certificates",
		"alpine:latest",
		"test", "-f", "/certificates/AliasProjectCASelfSigned.pem",
	)
	return err == nil
}

// certificateExists checks if a domain certificate exists in volume
func (m *Manager) certificateExists(domain string) bool {
	// Check for both certificate and key
	certCheck := m.docker.Run("run", "--rm",
		"-v", "devbox_certs:/certificates",
		"alpine:latest",
		"test", "-f", fmt.Sprintf("/certificates/%s.crt", domain),
	)

	keyCheck := m.docker.Run("run", "--rm",
		"-v", "devbox_certs:/certificates",
		"alpine:latest",
		"test", "-f", fmt.Sprintf("/certificates/%s.key", domain),
	)

	return certCheck == nil && keyCheck == nil
}

func (m *Manager) generateCA() error {
	return m.docker.Run("run", "--rm",
		"-e", "DOMAIN=init",
		"-v", "devbox_certs:/certificates",
		"ghcr.io/aliasproject/openssl:latest",
	)
}

func (m *Manager) copyCAToHost() error {
	// Ensure certs directory exists
	os.MkdirAll(m.config.CertsDir, 0755)

	// Copy CA from volume to host
	return m.docker.Run("run", "--rm",
		"-v", "devbox_certs:/source",
		"-v", m.config.DevboxDir+":/dest",
		"alpine:latest",
		"cp", "/source/AliasProjectCASelfSigned.pem", "/dest/certs/AliasProjectCASelfSigned.pem",
	)
}

func (m *Manager) createNginxVhost(projectName, domain string) error {
	vhostConfig := fmt.Sprintf(`server {
    listen 80;
    server_name %s;
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name %s;

    ssl_certificate /etc/nginx/ssl/%s.crt;
    ssl_certificate_key /etc/nginx/ssl/%s.key;

    # SSL configuration
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;
    ssl_prefer_server_ciphers on;

    # Proxy configuration
    location / {
        proxy_pass http://devbox_php_%s:80;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header X-Forwarded-Port $server_port;

        # WebSocket support
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # Timeouts
        proxy_connect_timeout 60s;
        proxy_send_timeout 60s;
        proxy_read_timeout 60s;

        # Buffer settings
        proxy_buffering off;
        proxy_buffer_size 4k;
        proxy_buffers 8 4k;
    }
}`, domain, domain, domain, domain, projectName)

	// Write vhost configuration to nginx container
	return m.docker.CopyToContainer("devbox_nginx",
		"/etc/nginx/conf.d/"+projectName+".conf",
		[]byte(vhostConfig),
	)
}

func (m *Manager) reloadNginx() error {
	return m.docker.Run("exec", "devbox_nginx", "nginx", "-s", "reload")
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
	// Copy to system certificate store
	destPath := "/usr/local/share/ca-certificates/devbox-ca.crt"

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
		return fileExists("/usr/local/share/ca-certificates/devbox-ca.crt")
	case "windows":
		cmd := exec.Command("powershell", "-Command",
			"Get-ChildItem -Path Cert:\\LocalMachine\\Root | Where-Object { $_.Subject -like '*AliasProjectCASelfSigned*' }")
		return cmd.Run() == nil
	default:
		return false
	}
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
		if fileExists("/usr/local/share/ca-certificates/devbox-ca.crt") {
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
