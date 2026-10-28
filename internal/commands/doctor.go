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

	"github.com/aliasproject/ingot/internal/config"
	"github.com/aliasproject/ingot/internal/project"
	"github.com/aliasproject/ingot/internal/services"
	"github.com/aliasproject/ingot/internal/utils"
)

// doctorProbeImage is the image used to test registry read access. It's the
// smallest image ingot always needs, so if this can't be read nothing else
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
		fmt.Println(utils.Colorize("[INFO]", utils.ColorBlue) + " Warnings won't stop ingot from working but may explain odd behaviour")
	} else {
		fmt.Println(utils.Colorize("[SUCCESS]", utils.ColorGreen) + " Everything looks healthy")
	}
	return nil
}

func doctorHeader() {
	fmt.Printf("Ingot v%s - Doctor\n", config.Version)
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

// handleDoctor runs every environment check ingot depends on and prints a
// pass/warn/fail line for each, with a fix for anything that isn't OK.
// It's read-only: nothing is started, pulled, or changed.
func (h *Handler) handleDoctor() error {
	doctorHeader()
	r := &doctorReport{}

	h.doctorDocker(r)
	h.doctorInstall(r)
	registryOK := h.doctorRegistry(r)
	h.doctorNetwork(r)
	h.doctorServices(r)
	h.doctorSSL(r)
	h.doctorProject(r, registryOK)
	h.doctorShare(r)
	h.doctorIdle(r)

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
		r.ok("Binary installed at %s", config.IngotBinary)
	} else {
		r.fail(fmt.Sprintf("Binary not installed at %s", config.IngotBinary), "ingot install")
	}

	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		if exe != config.IngotBinary {
			r.info("Running %s rather than the installed binary", exe)
		}
	}

	if _, err := exec.LookPath("ingot"); err != nil {
		r.warn("'ingot' is not on your PATH", fmt.Sprintf("add %s to PATH", filepath.Dir(config.IngotBinary)))
	}
}

// doctorRegistry checks that ghcr.io serves ingot's images, and returns
// whether it does so the project section can skip a doomed registry probe.
// The images are public, so no login is involved; the usual cause of a
// refusal is a ghcr.io login left in Docker's config (older ingot versions
// ran `docker login ghcr.io`) whose token has since been revoked or expired.
// Docker sends stored credentials even for a public image, and GHCR answers
// a bad one with "denied" rather than falling back to anonymous access.
func (h *Handler) doctorRegistry(r *doctorReport) bool {
	r.section("REGISTRY")

	if err := h.docker.CheckRegistryAccess(doctorProbeImage); err != nil {
		fix := "check this machine can reach ghcr.io"
		lower := strings.ToLower(err.Error())
		if dockerLoggedIntoGHCR() && (strings.Contains(lower, "denied") || strings.Contains(lower, "unauthorized")) {
			fix = "docker logout ghcr.io   (Docker's saved ghcr.io login is being refused; ingot's images are public and need none)"
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
		r.fail(fmt.Sprintf("Docker network %s missing", bridge), "ingot global start (recreates it)")
	}

	if hostIP, err := services.GetHostIP(); err == nil {
		r.ok("Host IP detected as %s", hostIP)
	} else {
		r.warn(fmt.Sprintf("Could not detect host IP: %v", err), "check that the machine has a default route")
	}

	if configured, detail := utils.TestDomainResolverStatus(); configured {
		r.ok("Host resolver routes .test to ingot_dns (%s)", detail)
	} else {
		r.fail("Host resolver not configured for .test ("+detail+")", "ingot dns-setup")
	}

	probe := "ingot-doctor-probe.test"
	if addrs, err := lookupVia(probe, "127.0.0.1:5354"); err == nil && len(addrs) > 0 {
		r.ok("ingot_dns answers for .test on 127.0.0.1:5354 (%s -> %s)", probe, addrs[0])
	} else {
		r.fail("ingot_dns is not answering on 127.0.0.1:5354", "ingot global restart")
	}

	if addrs, err := lookupVia(probe, ""); err == nil && len(addrs) > 0 {
		r.ok("System resolver resolves .test (%s -> %s)", probe, addrs[0])
	} else {
		r.fail("System resolver does not resolve .test domains (browsers won't reach https://<project>.test)",
			"ingot dns-setup, then restart the resolver (systemd-resolved/dnsmasq)")
	}
}

// lookupVia resolves a name either through the system resolver (empty
// server) or by sending the query straight to one nameserver, so the report
// can tell "ingot_dns is down" apart from "the host isn't asking it".
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
		r.fail(fmt.Sprintf("Could not inspect global services: %v", err), "ingot global start")
		return
	}
	for _, svc := range svcs {
		if svc.Container == services.IdleContainer {
			continue // optional; checked in its own section (doctorIdle)
		}
		if svc.Status == "running" {
			r.ok("%s running (%s)", svc.Name, svc.Container)
			continue
		}
		r.fail(fmt.Sprintf("%s is %s (%s)", svc.Name, svc.Status, svc.Container), "ingot global start")
	}

	for _, volume := range []string{"ingot_certs", "ingot_vhosts"} {
		if h.docker.VolumeExists(volume) {
			r.ok("Volume %s exists", volume)
		} else {
			r.fail(fmt.Sprintf("Volume %s missing", volume), "ingot global start")
		}
	}
}

func (h *Handler) doctorSSL(r *doctorReport) {
	r.section("SSL")

	if h.ssl.CAExists() {
		r.ok("CA certificate present in ingot_certs")
	} else {
		r.fail("CA certificate missing from ingot_certs", "ingot cert install (generates the CA first)")
		return
	}

	if h.ssl.IsCertificateInstalled() {
		r.ok("CA trusted by the system certificate store")
	} else {
		r.fail("CA not trusted by the system certificate store (browsers will warn on https://*.test)", "ingot cert install")
	}

	if present, checkable, installed := h.ssl.ChromeNSSDBStatus(); present {
		switch {
		case !checkable:
			r.warn("Chrome's NSS database (~/.pki/nssdb) exists but certutil isn't installed, so its trust can't be checked or set",
				"install the 'nss' package (provides certutil), then ingot cert install")
		case installed:
			r.ok("CA trusted in Chrome's NSS database (~/.pki/nssdb)")
		default:
			r.warn("CA missing from Chrome's NSS database (~/.pki/nssdb) - Chrome/Chromium ignore the system store on Linux",
				"ingot cert install, then fully quit and reopen Chrome")
		}
	}
}

// doctorProject checks the project in the current directory. For an
// uninitialized one it pre-flights what `ingot start` would do - which
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
		h.doctorImage(r, image, registryOK, "ingot start would use")
		r.info("Run 'ingot start' to create it")
		return
	}

	if proj.Status == "running" {
		r.ok("Container %s running (PHP %s)", proj.Container, proj.PHPVersion)
	} else {
		r.warn(fmt.Sprintf("Container %s is %s", proj.Container, proj.Status), "ingot start")
	}

	image := h.projects.ImageReference(proj)
	if proj.ImageType == "custom" && proj.ImageSource == "build" {
		if h.docker.ImageExists(image) {
			r.ok("Locally built image %s present", image)
		} else {
			r.fail(fmt.Sprintf("Locally built image %s missing", image), "ingot rebuild")
		}
	} else {
		h.doctorImage(r, image, registryOK, "project uses")
	}

	if info, err := h.docker.InspectContainer(proj.Container); err == nil {
		// Versions before ALI-837 stored every build-arg value in this
		// label, credentials included.
		if stored := project.StoredSecretBuildArgs(info.Labels); len(stored) > 0 {
			r.warn(fmt.Sprintf("Container label stores the value of build arg %s (readable with docker inspect)", strings.Join(stored, ", ")),
				"revoke that credential, then ingot rebuild (it asks for the new value and doesn't save it)")
		}

		bridge := config.GetNetworkConfig().BridgeName
		if utils.StringSliceContains(info.Networks, bridge) {
			r.ok("Container attached to %s", bridge)
		} else {
			r.fail(fmt.Sprintf("Container not on %s (Caddy can't reach it)", bridge), "ingot rebuild")
		}
	}

	domain := proj.Name + ".test"
	if vhost, err := h.docker.ReadFileFromVolume("ingot_vhosts", proj.Name+".caddyfile"); err == nil {
		r.ok("Caddy vhost %s.caddyfile present", proj.Name)
		switch {
		case strings.Contains(vhost, "import ingot_stopped"):
			r.info("Stopped with 'ingot stop': visits won't start it until 'ingot start'")
		case !strings.Contains(vhost, "import ingot_wake"):
			r.warn("Vhost predates scale to zero, so visiting the project while it's asleep won't start it",
				"ingot cert repair")
		}
	} else {
		r.fail("Caddy vhost missing (https://"+domain+" will hit the 'No project here' page)", "ingot cert repair")
	}

	if h.ssl.CertificateExists(domain) {
		r.ok("Certificate for %s present", domain)
	} else {
		r.fail("Certificate for "+domain+" missing", "ingot cert repair")
	}

	if proj.Status == "running" {
		if code, err := probeHTTPS(proj.URL); err != nil {
			r.fail(fmt.Sprintf("%s unreachable: %v", proj.URL, err), "ingot global restart, then ingot restart")
		} else if code >= 500 {
			r.warn(fmt.Sprintf("%s responded %d (Caddy is up but the app errored)", proj.URL, code), "ingot logs")
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
				"fix the REGISTRY section above; ingot can't pull this image until then")
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
		r.fail(fmt.Sprintf("INGOT_SHARE_DOMAIN %q isn't a valid domain, so Caddy will reject named shares", h.config.ShareDomain),
			"set it to the bare domain, e.g. acme-dev.com")
	case token && domain:
		r.ok("Named tunnel configured (https://<project>.%s)", h.config.ShareDomain)
	case token != domain:
		r.warn("Only one of INGOT_SHARE_TUNNEL_TOKEN / INGOT_SHARE_DOMAIN is set, so shares use quick tunnels",
			"set both for stable named-tunnel URLs, or unset the other (see docs/SHARING.md)")
	default:
		r.info("Using quick tunnels (set INGOT_SHARE_TUNNEL_TOKEN and INGOT_SHARE_DOMAIN for stable URLs)")
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
				fmt.Sprintf("cd into %s and run 'ingot share stop' (then 'ingot share' to reshare)", sh.Project))
		}
	}
}
