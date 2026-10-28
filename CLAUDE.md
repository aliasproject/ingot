# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Ingot (`ingot`) is a single-binary Go CLI that manages a local Docker-based PHP development environment: per-project PHP (FrankenPHP) containers, global shared services (DNS, Caddy reverse proxy, Mailpit), optional MySQL/PostgreSQL databases, and locally-trusted SSL certificates for `*.test` domains. It has no server component — every command shells out to the `docker` CLI on the user's machine.

Ingot is a standalone tool for any PHP developer. It started as the local-dev companion to Alios, Alias Project's server-management platform, and was renamed and split out so it can be used without Alios. That's why some code comments refer to "production Alios" or "the Alios platform repo": the conventions here (image-selection vocabulary, Caddy as the reverse proxy, env injection) were copied from it so local projects look like production. There is no code dependency on Alios, and none should be added. Alios-specific features belong in a future Alios CLI that can build on Ingot.

## Build & run

```bash
go build -o ingot ./cmd             # build the binary
go run ./cmd <command>               # run without building, e.g. go run ./cmd status
go vet ./...                         # static checks
```

`go test ./...` covers only a few pure functions so far; there's no linter/Makefile. Most changes are verified with `go build`/`go vet` and by running the CLI.

Releases are cut with `scripts/release.sh <version> [notes]` (run from `scripts/`, on a clean `main`). It patches `internal/config.Version` via `sed` and commits that to `main`, cross-compiles for darwin/linux/windows (amd64, plus arm64 for darwin/linux), builds and pushes the `ingot-idle` and `ingot-openssl` images for that version to GHCR (amd64+arm64), then pushes a `v<version>` tag and publishes a GitHub release with the binaries, which is what the self-updater reads. `docs/UPDATES.md` describes an older manifest/S3 design that the code no longer uses.

## Architecture

**Entry point**: `cmd/main.go` builds a `config.Config` (singleton, env-var driven) and a `docker.Client`, then hands off to `commands.Handler`, which is the dispatch table for every CLI subcommand (`internal/commands/handler.go`). First run (no binary at `/usr/local/bin/ingot`) triggers `commands.Installer` (`internal/commands/installer.go`) instead of normal command dispatch.

**No persisted config files.** This is deliberate, not incomplete: `config.Config.Save()` is a no-op. All state lives in one of two places:
- Environment variables (`INGOT_*`, plus an optional `GITHUB_TOKEN` that only the self-updater uses) for user settings — see `internal/config/config.go`.
- Docker container labels (`ingot.project.name`, `ingot.project.php_version`, `ingot.project.server_root`, `ingot.project.image_type`, `ingot.project.framework`, etc.) for per-project state — projects are *discovered* by inspecting containers, not read from disk. `project.Manager.GetCurrentProject()` derives project identity from the current working directory name plus the `ingot_php_<name>` container's labels/state; there is no separate "project database."

**`internal/docker/client.go`** is the only layer that shells out to the `docker` binary (`exec.Command`). Every other package goes through `*docker.Client` rather than calling `os/exec` directly for container operations.

**Two tiers of containers**, both Docker-labeled so they're discoverable without local state:
- *Project containers* (`internal/project/manager.go`): one `ingot_php_<project>` container per project directory, created directly via `docker.Client.RunContainer` (not compose) — or, for a project with `image_type=custom` and a build-from-Dockerfile choice, via `docker.Client.BuildImage` first. Changing PHP version, image choice, or server root means destroying and recreating the container, not reconfiguring it in place.
- *Global services* (`internal/services/manager.go`): `ingot_dns`, `ingot_proxy`, `ingot_mail`, plus any configured databases, all managed as one Docker Compose project (`name: ingot`) whose YAML is generated inline in Go (`generateComposeConfig`) rather than read from a file. Adding/removing a database rewrites and re-applies the whole compose config, which restarts the group.

**Images come from public GHCR packages** (`ghcr.io/aliasproject/{php,dns,caddy,ingot-idle,ingot-openssl}`), pulled anonymously: Ingot needs no GitHub account or `docker login`, and there's deliberately no GitHub auth code. The one GitHub token use left is the self-updater's optional `GITHUB_TOKEN`/`gh auth token` (`commands.ensureUpdaterAuthenticated`). A pull that GHCR refuses with "denied" is almost always a stale `ghcr.io` login in Docker's config (older versions ran `docker login ghcr.io`), which Docker still sends for public images; `ingot doctor` and `project.Manager.pullOrUseLocal` point at `docker logout ghcr.io`. The `php` image comes in several variant tags per PHP version (`-frankenphp-dev`, `-frankenphp-wordpress-dev`, ...), selected in `project.Manager.createContainer` based on the project's detected/chosen `framework` and `image_type` labels.

**SSL** (`internal/ssl/manager.go`): the "Ingot Local CA" and per-domain certs are generated by running `ghcr.io/aliasproject/ingot-openssl:<version>` (`Dockerfile.openssl` + `openssl/entrypoint.sh`) against the `ingot_certs` volume, not a Go TLS library. Self-signed, because `.test` is IANA-reserved so ACME can't work. Per-project vhosts (`<project>.caddyfile`) are written into the `ingot_vhosts` volume (mounted at `/etc/caddy` in `ingot_proxy`; the base `Caddyfile` from `services.Manager.Start` does `import /etc/caddy/*.caddyfile`), so adding/removing a project is one file plus `caddy reload`. Vhosts are plain `fmt.Sprintf` strings, no templating. Host-less catch-all sites serve a "No project here" 404 (`internal/services/default_page.html`); its HTTPS side uses Caddy's on-demand internal issuer with the ingot CA as root (`pki` block; `ssl.Manager.PrepareCAForCaddy`), because browsers reject a static wildcard directly under a TLD.

**Sharing** (`internal/share/manager.go`, `ingot share`): exposes a project through a Cloudflare Tunnel. A `cloudflared` container on `ingot_internal` sends plain HTTP to Caddy, and a `<project>.share.caddyfile` vhost (the only record of the share's URL, mode and auth) proxies to the project. Tunnel containers carry `ingot.share.*` labels only for cleanup and aren't global services, so `global restart` doesn't bounce them. Quick mode (default): one `ingot_share_<project>` container per share, random trycloudflare.com URL. Named mode (`INGOT_SHARE_TUNNEL_TOKEN` + `INGOT_SHARE_DOMAIN`): one shared `ingot_share` container, `*.<domain>` ingress set in the Cloudflare dashboard. See `docs/SHARING.md`.

**Scale to zero** (`internal/idle`, `cmd/ingot-idle`, `ingot idle`): the optional `ingot_idle` service stops project containers idle for `INGOT_IDLE_TIMEOUT` (default 30m; per-project `ingot.project.idle` label overrides) and restarts them when Caddy can't reach them. Every project/share vhost ends with `import ingot_wake <project>`, which sends 502/503/504 to the waker, which starts the container and 307-redirects back. `ingot stop` swaps it for `import ingot_stopped <project>` so a hand-stopped project stays down until `ingot start`; the vhost is the only record of that state (`ssl.SetProjectStopped`). Idle = no network traffic, CPU or exec'd processes (`idle.Tracker`, unit-tested). It is deliberately not a compose service or in `GlobalServices` (compose aborts the whole `up` if one image won't pull; see `services/idle.go`), runs from its own image (`Dockerfile.idle` -> `ghcr.io/aliasproject/ingot-idle:<version>`), and is the one place that uses the Docker Engine API over the socket instead of `docker.Client` (scratch image, no CLI). See `docs/IDLE.md`.

**Networking**: all ingot containers share one Docker bridge network (`ingot_internal`, see `config.GetNetworkConfig()`). The `ingot_dns` container resolves `*.test` to the host so `https://<project>.test` reaches Caddy, which reverse-proxies to `ingot_php_<project>` by container name (Caddy resolves Docker container names on the shared network natively — no explicit resolver directive needed). Project containers get `ingot_dns`'s live container IP injected as their DNS server (resolved at container-creation time, not hardcoded) so they can resolve other `.test` domains from inside.

**Self-update** (`internal/updater/updater.go`): reads the latest GitHub Release and downloads the asset for this OS/arch. While the repo is private it needs a token (`GITHUB_TOKEN`, else `gh auth token`); there's no other update source. Replaces the running binary atomically, escalating to `sudo`/Windows UAC if the install path isn't writable.

## Working in this codebase

- Respect the "no local config files" invariant — new persistent settings belong in env vars (global) or Docker labels (per-project/per-container), not in a JSON/YAML file on disk.
- `internal/commands/handler.go` mixes command parsing, business-logic orchestration, and terminal output (colorized `[STATUS]`-style prefixes via `utils.Colorize`) in one file; follow that existing style rather than introducing a separate output/formatting layer.
- Anything that talks to Docker should go through `internal/docker.Client`, not raw `exec.Command`.
- `main.go.backup` at the repo root is a stale leftover, not part of the build — ignore it rather than editing it.
