package commands

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aliasproject/alios-cli/internal/config"
	"github.com/aliasproject/alios-cli/internal/github"
	"github.com/aliasproject/alios-cli/internal/project"
	"github.com/aliasproject/alios-cli/internal/services"
	"github.com/aliasproject/alios-cli/internal/utils"
)

// doctorProbeImage is the image used to test registry read access. It's the
// smallest image alios always needs, so if this can't be read nothing else
// from ghcr.io/aliasproject can either.
const doctorProbeImage = "ghcr.io/aliasproject/dns:latest"

// doctorReport tallies check results and renders them in the same
// bracket-prefixed style as the rest of the CLI. Each failing check carries
// a concrete fix so the report is actionable, not just descriptive.
type doctorReport struct {
	passed, warned, failed int
}

func (r *doctorReport) section(title string) {
	fmt.Printf("\n%s\n", utils.Colorize("["+title+"]", utils.ColorCyan))
}

func (r *doctorReport) ok(format string, args ...interface{}) {
	r.passed++
	fmt.Printf("  %s   %s\n", utils.Colorize("[OK]", utils.ColorGreen), fmt.Sprintf(format, args...))
}

// info prints a neutral line that isn't a check and doesn't affect the tally.
func (r *doctorReport) info(format string, args ...interface{}) {
	fmt.Printf("  %s   %s\n", utils.Colorize("[--]", utils.ColorGray), fmt.Sprintf(format, args...))
}

func (r *doctorReport) warn(msg, fix string) {
	r.warned++
	fmt.Printf("  %s %s\n", utils.Colorize("[WARN]", utils.ColorYellow), msg)
	if fix != "" {
		fmt.Printf("         fix: %s\n", fix)
	}
}

func (r *doctorReport) fail(msg, fix string) {
	r.failed++
	fmt.Printf("  %s %s\n", utils.Colorize("[FAIL]", utils.ColorRed), msg)
	if fix != "" {
		fmt.Printf("         fix: %s\n", fix)
	}
}

func (r *doctorReport) summary() error {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("%d passed, %d warnings, %d failed\n", r.passed, r.warned, r.failed)
	if r.failed > 0 {
		return fmt.Errorf("%d check(s) failed", r.failed)
	}
	if r.warned > 0 {
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Warnings won't stop alios from working but may explain odd behaviour")
	} else {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Everything looks healthy")
	}
	return nil
}

func doctorHeader() {
	fmt.Printf("Alios v%s - Doctor\n", config.Version)
	fmt.Println(strings.Repeat("=", 50))
}

// DoctorWithoutDocker is the report main() prints when the Docker client
// itself can't be built - every other check depends on Docker, so there's
// nothing else worth probing until that's fixed.
func DoctorWithoutDocker(dockerErr error) {
	doctorHeader()
	r := &doctorReport{}
	r.section("DOCKER")
	if _, err := exec.LookPath("docker"); err != nil {
		r.fail("docker binary not found in PATH", "install Docker (https://docs.docker.com/get-docker/) and make sure 'docker' is on your PATH")
	} else {
		r.fail(fmt.Sprintf("Docker daemon not reachable: %v", dockerErr), dockerFix())
	}
	r.summary()
}

func dockerFix() string {
	switch runtime.GOOS {
	case "darwin":
		return "start Docker Desktop and wait for it to report 'running'"
	case "linux":
		return "sudo systemctl start docker, and make sure your user is in the 'docker' group (then log out and back in)"
	default:
		return "start Docker Desktop"
	}
}

// handleDoctor runs every environment check alios depends on and prints a
// pass/warn/fail line for each, with a fix for anything that isn't OK.
// It's read-only: nothing is started, pulled, or changed.
func (h *Handler) handleDoctor() error {
	doctorHeader()
	r := &doctorReport{}

	h.doctorDocker(r)
	h.doctorInstall(r)
	tokenOK := h.doctorGitHub(r)
	h.doctorNetwork(r)
	h.doctorServices(r)
	h.doctorSSL(r)
	h.doctorProject(r, tokenOK)
	h.doctorShare(r)

	return r.summary()
}

func (h *Handler) doctorDocker(r *doctorReport) {
	r.section("DOCKER")

	if version, err := h.docker.ServerVersion(); err == nil {
		r.ok("Docker daemon responding (server %s, cli at %s)", version, h.docker.Path())
	} else {
		r.fail(fmt.Sprintf("Docker daemon not responding: %v", err), dockerFix())
		return
	}

	if version, err := h.docker.ComposeVersion(); err == nil {
		r.ok("Docker Compose plugin available (%s)", version)
	} else {
		r.fail("Docker Compose plugin not available (global services are a compose project)",
			"install the docker-compose plugin (docker-compose-plugin package, or bundled with Docker Desktop)")
	}
}

func (h *Handler) doctorInstall(r *doctorReport) {
	r.section("INSTALL")

	if h.config.IsInstalled() {
		r.ok("Binary installed at %s", config.AliosBinary)
	} else {
		r.fail(fmt.Sprintf("Binary not installed at %s", config.AliosBinary), "alios install")
	}

	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if exe != config.AliosBinary {
			r.info("Running %s rather than the installed binary", exe)
		}
	}

	if _, err := exec.LookPath("alios"); err != nil {
		r.warn("'alios' is not on your PATH", fmt.Sprintf("add %s to PATH", filepath.Dir(config.AliosBinary)))
	}
}

// doctorGitHub verifies the full chain from "a token exists" through "the
// registry will actually serve us an image", and returns whether the last
// link holds so the project section can skip a doomed registry probe.
func (h *Handler) doctorGitHub(r *doctorReport) bool {
	r.section("GITHUB")

	token, source := h.github.FindToken()
	if token == "" {
		r.fail("No GitHub token found (checked GITHUB_TOKEN and 'gh auth token')",
			"alios github-auth, or export GITHUB_TOKEN=<token with repo + read:packages>")
		return false
	}
	r.ok("Token found via %s", source)

	if err := h.github.ValidateToken(token); err != nil {
		r.fail(fmt.Sprintf("Token rejected by GitHub: %v", err), "alios github-auth (or 'gh auth login' again)")
		return false
	}
	r.ok("Token valid for GitHub user %s", h.github.Username(token))

	scopeOK := true
	scopes, reported, err := h.github.TokenScopes(token)
	switch {
	case err != nil:
		r.warn(fmt.Sprintf("Could not read token scopes: %v", err), "")
	case !reported:
		r.warn("Token does not report scopes (fine-grained token?) - can't confirm read:packages", "if pulls are denied, grant the token package read access")
	case github.HasPackagesScope(scopes):
		r.ok("Token scopes include package read access (%s)", strings.Join(scopes, ", "))
	default:
		scopeOK = false
		r.fail(fmt.Sprintf("Token scopes (%s) lack read:packages - ghcr.io pulls will be denied", strings.Join(scopes, ", ")),
			"gh auth refresh -h github.com -s read:packages   (or use a GITHUB_TOKEN that has read:packages)")
	}

	if dockerLoggedIntoGHCR() {
		r.ok("Docker has stored credentials for ghcr.io")
	} else {
		r.fail("Docker is not logged into ghcr.io", "alios github-auth (runs 'docker login ghcr.io' with your token)")
	}

	if err := h.docker.CheckRegistryAccess(doctorProbeImage); err != nil {
		fix := "alios github-auth"
		if !scopeOK {
			fix = "grant read:packages (see above), then alios github-auth to re-login docker"
		} else if strings.Contains(strings.ToLower(err.Error()), "denied") {
			fix = "the token's owner may need access to the aliasproject packages, or SSO authorization for the org"
		}
		r.fail(fmt.Sprintf("Registry refused %s: %v", doctorProbeImage, err), fix)
		return false
	}
	r.ok("Registry access confirmed (%s readable)", doctorProbeImage)
	return true
}

// dockerLoggedIntoGHCR checks Docker's own config for a ghcr.io entry. With
// a credential helper the secret lives elsewhere but the "auths" key is
// still recorded, so this works for both plain and helper-backed setups.
func dockerLoggedIntoGHCR() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(home, ".docker", "config.json")
	if custom := os.Getenv("DOCKER_CONFIG"); custom != "" {
		path = filepath.Join(custom, "config.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"ghcr.io"`)
}

func (h *Handler) doctorNetwork(r *doctorReport) {
	r.section("NETWORK")

	bridge := config.GetNetworkConfig().BridgeName
	if h.docker.NetworkExists(bridge) {
		r.ok("Docker network %s exists", bridge)
	} else {
		r.fail(fmt.Sprintf("Docker network %s missing", bridge), "alios global start (recreates it)")
	}

	if hostIP, err := services.GetHostIP(); err == nil {
		r.ok("Host IP detected as %s", hostIP)
	} else {
		r.warn(fmt.Sprintf("Could not detect host IP: %v", err), "check that the machine has a default route")
	}

	if configured, detail := utils.TestDomainResolverStatus(); configured {
		r.ok("Host resolver routes .test to alios_dns (%s)", detail)
	} else {
		r.fail("Host resolver not configured for .test ("+detail+")", "alios dns-setup")
	}

	probe := "alios-doctor-probe.test"
	if addrs, err := lookupVia(probe, "127.0.0.1:5354"); err == nil && len(addrs) > 0 {
		r.ok("alios_dns answers for .test on 127.0.0.1:5354 (%s -> %s)", probe, addrs[0])
	} else {
		r.fail("alios_dns is not answering on 127.0.0.1:5354", "alios global restart")
	}

	if addrs, err := lookupVia(probe, ""); err == nil && len(addrs) > 0 {
		r.ok("System resolver resolves .test (%s -> %s)", probe, addrs[0])
	} else {
		r.fail("System resolver does not resolve .test domains (browsers won't reach https://<project>.test)",
			"alios dns-setup, then restart the resolver (systemd-resolved/dnsmasq)")
	}
}

// lookupVia resolves a name either through the system resolver (empty
// server) or by sending the query straight to one nameserver, so the report
// can tell "alios_dns is down" apart from "the host isn't asking it".
func lookupVia(name, server string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resolver := &net.Resolver{}
	if server != "" {
		resolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 2 * time.Second}
				return d.DialContext(ctx, "udp", server)
			},
		}
	}
	return resolver.LookupHost(ctx, name)
}

func (h *Handler) doctorServices(r *doctorReport) {
	r.section("SERVICES")

	svcs, err := h.services.Status()
	if err != nil {
		r.fail(fmt.Sprintf("Could not inspect global services: %v", err), "alios global start")
		return
	}
	for _, svc := range svcs {
		if svc.Status == "running" {
			r.ok("%s running (%s)", svc.Name, svc.Container)
			continue
		}
		r.fail(fmt.Sprintf("%s is %s (%s)", svc.Name, svc.Status, svc.Container), "alios global start")
	}

	for _, volume := range []string{"alios_certs", "alios_vhosts"} {
		if h.docker.VolumeExists(volume) {
			r.ok("Volume %s exists", volume)
		} else {
			r.fail(fmt.Sprintf("Volume %s missing", volume), "alios global start")
		}
	}
}

func (h *Handler) doctorSSL(r *doctorReport) {
	r.section("SSL")

	if h.ssl.CAExists() {
		r.ok("CA certificate present in alios_certs")
	} else {
		r.fail("CA certificate missing from alios_certs", "alios cert install (generates the CA first)")
		return
	}

	if h.ssl.IsCertificateInstalled() {
		r.ok("CA trusted by the system certificate store")
	} else {
		r.fail("CA not trusted by the system certificate store (browsers will warn on https://*.test)", "alios cert install")
	}

	if present, checkable, installed := h.ssl.ChromeNSSDBStatus(); present {
		switch {
		case !checkable:
			r.warn("Chrome's NSS database (~/.pki/nssdb) exists but certutil isn't installed, so its trust can't be checked or set",
				"install the 'nss' package (provides certutil), then alios cert install")
		case installed:
			r.ok("CA trusted in Chrome's NSS database (~/.pki/nssdb)")
		default:
			r.warn("CA missing from Chrome's NSS database (~/.pki/nssdb) - Chrome/Chromium ignore the system store on Linux",
				"alios cert install, then fully quit and reopen Chrome")
		}
	}
}

// doctorProject checks the project in the current directory. For an
// uninitialized one it pre-flights what `alios start` would do - which
// image it would use and whether that image is reachable - since that's
// exactly the failure that's hardest to read from init's own output.
func (h *Handler) doctorProject(r *doctorReport, registryOK bool) {
	proj, err := h.projects.GetCurrentProject()
	if err != nil {
		r.section("PROJECT")
		r.warn(fmt.Sprintf("Could not determine current project: %v", err), "")
		return
	}
	r.section("PROJECT " + proj.Name)

	if proj.Status == "not initialized" {
		r.info("Not initialized in this directory (%s)", proj.Path)
		if proj.Framework == "" {
			proj.Framework = project.DetectFramework(proj.Path)
		}
		if proj.Framework != "" {
			r.ok("Detected framework: %s", proj.Framework)
		}
		image := h.projects.ImageReference(proj)
		h.doctorImage(r, image, registryOK, "alios start would use")
		r.info("Run 'alios start' to create it")
		return
	}

	if proj.Status == "running" {
		r.ok("Container %s running (PHP %s)", proj.Container, proj.PHPVersion)
	} else {
		r.warn(fmt.Sprintf("Container %s is %s", proj.Container, proj.Status), "alios start")
	}

	image := h.projects.ImageReference(proj)
	if proj.ImageType == "custom" && proj.ImageSource == "build" {
		if h.docker.ImageExists(image) {
			r.ok("Locally built image %s present", image)
		} else {
			r.fail(fmt.Sprintf("Locally built image %s missing", image), "alios rebuild")
		}
	} else {
		h.doctorImage(r, image, registryOK, "project uses")
	}

	if info, err := h.docker.InspectContainer(proj.Container); err == nil {
		bridge := config.GetNetworkConfig().BridgeName
		if utils.StringSliceContains(info.Networks, bridge) {
			r.ok("Container attached to %s", bridge)
		} else {
			r.fail(fmt.Sprintf("Container not on %s (Caddy can't reach it)", bridge), "alios rebuild")
		}
	}

	domain := proj.Name + ".test"
	if _, err := h.docker.ReadFileFromVolume("alios_vhosts", proj.Name+".caddyfile"); err == nil {
		r.ok("Caddy vhost %s.caddyfile present", proj.Name)
	} else {
		r.fail("Caddy vhost missing (https://"+domain+" will hit the 'No project here' page)", "alios cert repair")
	}

	if h.ssl.CertificateExists(domain) {
		r.ok("Certificate for %s present", domain)
	} else {
		r.fail("Certificate for "+domain+" missing", "alios cert repair")
	}

	if proj.Status == "running" {
		if code, err := probeHTTPS(proj.URL); err != nil {
			r.fail(fmt.Sprintf("%s unreachable: %v", proj.URL, err), "alios global restart, then alios restart")
		} else if code >= 500 {
			r.warn(fmt.Sprintf("%s responded %d (Caddy is up but the app errored)", proj.URL, code), "alios logs")
		} else {
			r.ok("%s responded %d", proj.URL, code)
		}
	}
}

// doctorImage reports whether a registry image is available, first locally
// and then (only if registry access already passed) at the registry.
func (h *Handler) doctorImage(r *doctorReport, image string, registryOK bool, verb string) {
	local := h.docker.ImageExists(image)
	if local {
		r.ok("Image %s is cached locally (%s)", image, verb)
	}
	if !registryOK {
		if !local {
			r.fail(fmt.Sprintf("Image %s is not cached locally and registry access failed (%s)", image, verb),
				"fix the GITHUB section above; alios can't pull this image until then")
		}
		return
	}
	if err := h.docker.CheckRegistryAccess(image); err != nil {
		fix := "check the tag exists in ghcr.io/aliasproject/php, or choose a different PHP version"
		if local {
			r.warn(fmt.Sprintf("Image %s not readable at the registry (%v); the cached copy will be used", image, err), fix)
		} else {
			r.fail(fmt.Sprintf("Image %s not available at the registry: %v", image, err), fix)
		}
		return
	}
	r.ok("Image %s readable at the registry", image)
}

// probeHTTPS fetches a project URL through Caddy. TLS verification is
// skipped on purpose: trust is checked separately in the SSL section, and
// here the question is only whether Caddy routes the host to the container.
func probeHTTPS(url string) (int, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// doctorShare reports active shares and flags ones whose tunnel is gone, plus
// a half-configured named-tunnel setup (which silently falls back to quick
// tunnels).
func (h *Handler) doctorShare(r *doctorReport) {
	r.section("SHARE")

	token, domain := h.config.ShareTunnelToken != "", h.config.ShareDomain != ""
	switch {
	case token && domain && !h.config.ShareDomainValid():
		r.fail(fmt.Sprintf("ALIOS_SHARE_DOMAIN %q isn't a valid domain, so Caddy will reject named shares", h.config.ShareDomain),
			"set it to the bare domain, e.g. acme-dev.com")
	case token && domain:
		r.ok("Named tunnel configured (https://<project>.%s)", h.config.ShareDomain)
	case token != domain:
		r.warn("Only one of ALIOS_SHARE_TUNNEL_TOKEN / ALIOS_SHARE_DOMAIN is set, so shares use quick tunnels",
			"set both for stable named-tunnel URLs, or unset the other (see docs/SHARING.md)")
	default:
		r.info("Using quick tunnels (set ALIOS_SHARE_TUNNEL_TOKEN and ALIOS_SHARE_DOMAIN for stable URLs)")
	}

	shares, err := h.share.List()
	if err != nil {
		r.warn(fmt.Sprintf("Could not read shares: %v", err), "")
		return
	}
	if len(shares) == 0 {
		r.info("No projects are shared")
		return
	}
	for _, sh := range shares {
		if h.share.TunnelRunning(sh) {
			r.ok("%s shared at %s", sh.Project, sh.URL)
		} else {
			r.warn(fmt.Sprintf("%s has a share vhost but its tunnel isn't running", sh.Project),
				fmt.Sprintf("cd into %s and run 'alios share stop' (then 'alios share' to reshare)", sh.Project))
		}
	}
}
