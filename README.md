# Ingot

Ingot (`ingot`) is a single-binary local development environment manager
for PHP projects. Every project gets its own production-like container
(FrankenPHP behind a shared Caddy reverse proxy) with real, locally-trusted
HTTPS on a `*.test` domain, and projects nobody is using sleep until the
next visit.

No server component, no config files. Every command shells out to Docker on
your machine; all state lives in environment variables or Docker container
labels.

## What you get

- Per-project PHP (FrankenPHP) containers, one per project directory
- A shared Caddy reverse proxy with automatic HTTPS for `*.test` domains
  (self-signed, locally-trusted CA — no browser warnings)
- Shared DNS so `*.test` resolves without touching `/etc/hosts`
- A branded "No project here" page for any `*.test` domain that isn't running,
  instead of a bare connection error
- Optional MySQL/PostgreSQL, and MailHog for email testing
- Automatic WordPress detection (extra extensions, hardening, live-reload)
- A choice of prebuilt image, your project's own Dockerfile, or an arbitrary
  pulled image — per project, switchable any time
- Per-project env vars injected into the container, same as production
- Pause/resume everything with one command
- Scale to zero: idle projects are stopped and start again on the next
  visit (see [docs/IDLE.md](docs/IDLE.md))

## Installation

Download the binary for your platform from the latest release - e.g. for
Linux/amd64:

```bash
curl -fsSLo ingot https://github.com/aliasproject/ingot/releases/latest/download/ingot-linux-amd64
chmod +x ingot
./ingot
```

Other platforms: swap `ingot-linux-amd64` for `ingot-linux-arm64`,
`ingot-darwin-amd64`, `ingot-darwin-arm64`, or `ingot-windows-amd64.exe`.

On first run, `ingot` installs itself to `/usr/local/bin/ingot` and walks
you through database configuration and GitHub authentication (needed to pull
private images from GHCR). If it's interrupted partway through, just run
`ingot install` again — every step is safe to repeat, so it picks up from
wherever it left off rather than starting over.

### Requirements

- Docker installed and running
- macOS or Linux (Windows via WSL2)
- A GitHub account (for pulling private container images)

## Quick start

```bash
cd /path/to/your/project
ingot start
```

On the first `ingot start` for a project you'll be asked:

1. **How to build the image** — only if a `Dockerfile` is found in the
   project root: use it, use the prebuilt Ingot image, or pull a different
   image entirely. No Dockerfile → straight to prebuilt.
2. **PHP version** — only for the prebuilt path; a custom Dockerfile or
   pulled image brings its own PHP version.
3. **Server root** (defaults to `/app/public`).

None of this is asked again on later starts — it's stored on the project's
container labels. Your project is then available at `https://<name>.test`.

## Commands

### Project lifecycle

```
ingot start [version]    Start (or create) the project container
ingot stop               Stop the project container
ingot restart            Restart the project container
ingot remove / rm        Remove the project container
ingot status             Show detailed status for this project
ingot list / ls          List every project
```

### Changing configuration

```
ingot php [version]         Change PHP version (prebuilt projects only)
ingot server-root [path]    Change the server root directory
ingot image [mode]          Switch image mode without recreating from scratch
```

`ingot image` takes `prebuilt`, `dockerfile`, or `pull` (with a second arg
for the image to pull), or run it bare for an interactive prompt. Useful for
going back and forth while testing — e.g. `ingot image dockerfile` to try
your production build, `ingot image prebuilt` to switch back.

### Logs, exec, and shell

```
ingot logs
ingot exec composer install
ingot shell                # shell as your host user
ingot shell --user=root    # shell as root
```

### Global services

DNS, Caddy, MailHog, and any configured databases are shared across every
project:

```
ingot global start
ingot global stop
ingot global restart
ingot global status
```

### Scale to zero

Projects nobody is using are stopped after 30 minutes (`INGOT_IDLE_TIMEOUT`),
and the next request to one starts it again. The first page load takes a
second or two longer. `exec`, `shell` and `share` start a sleeping project
too. A project stopped with `ingot stop` stays down until `ingot start`. See
[docs/IDLE.md](docs/IDLE.md) for how idle is detected.

```
ingot idle            # show this project's setting
ingot idle off        # never stop it (e.g. it runs a queue worker)
ingot idle 2h         # its own timeout
ingot idle on         # back to INGOT_IDLE_TIMEOUT
```

### Pause & resume

Stop everything at once to free up CPU/RAM, then bring back exactly what was
running:

```
ingot pause
ingot resume
```

### Databases

```
ingot database add
ingot database remove
ingot database list
```

MySQL databases get an empty root password. PostgreSQL databases get user
`ingot`, password `ingot`, database `ingot`.

### SSL

```
ingot cert install    # Install the CA into your system trust store
ingot cert verify     # Check whether it's installed
ingot cert debug      # Detailed certificate/Caddy diagnostics
ingot cert repair     # Regenerate vhosts and restart Caddy
```

### Everything else

```
ingot status          Full system status (services, network, install)
ingot github-auth     (Re-)authenticate with GitHub
ingot update          Update to the latest version
ingot check-update    Check for an available update
ingot uninstall       Uninstall Ingot
ingot help            Show all commands
```

## Image selection

`ingot` mirrors production's own model for how a project's container image
is built:

- **Prebuilt** (default) — `ghcr.io/aliasproject/php:<version>-frankenphp-dev`,
  or the `-wordpress-dev` variant if a WordPress install is detected
  (`wp-config.php`/`wp-load.php` in the project root).
- **Custom Dockerfile** — builds from the project's own `Dockerfile`, the
  same one you'd actually deploy. If it declares build args with no default
  (`ARG SOME_TOKEN`, no `=value`), you're prompted for each one explicitly —
  nothing is silently reused from your GitHub auth or anything else.
- **Custom pull** — pulls a specific image you name.

Switch between them any time with `ingot image <mode>` — no need to remove
and recreate the project.

## Env vars

To inject environment variables into a project's container — the same way
production supplies a site's config as plain environment variables — set
them explicitly:

```
ingot env set APP_DEBUG=true DB_HOST=ingot_mysql
ingot env unset APP_DEBUG
ingot env list
```

Values live in the container's labels, not in a file, and `set`/`unset`
recreate the container so the change takes effect immediately (Docker has no
way to update a running container's environment in place, and neither does
production). A stopped project stays stopped.

Ingot deliberately does **not** read your project's `.env`. The project
directory is bind-mounted, so a framework that reads `.env` itself (Laravel,
for example) already sees every edit instantly with no restart. Injecting
those same keys as real env vars would only shadow the file with a stale
copy.

`SERVER_NAME`, `SERVER_ROOT`, `HTTPS`, and `PORT` are reserved for Ingot
itself and can't be set with `ingot env set`.

## Environment variables

Ingot has no config file — these are the only settings, read from your
shell environment:

```
GITHUB_TOKEN                 GitHub token for pulling private images
INGOT_DEFAULT_PHP_VERSION    Default PHP version for new projects
INGOT_HOST_IP                Override auto-detected host IP
INGOT_DNS_PORT               DNS listener port (default: 53)
INGOT_ENABLE_SSL             Enable/disable SSL setup (default: true)
INGOT_ENABLE_DNS             Enable/disable DNS setup (default: true)
INGOT_AUTO_START_GLOBAL      Auto-start global services (default: true)
INGOT_IDLE_TIMEOUT           Stop projects idle this long, e.g. 30m, 2h, or
                             off (default: 30m; applied by 'global restart',
                             kept when unset)
```

## Supported versions

- **PHP:** 8.5, 8.4 (default), 8.3, 8.2, 8.1, 8.0, 7.4
- **MySQL:** 8.0, 5.7
- **PostgreSQL:** 16, 15, 14, 13

## Building from source

```bash
git clone git@github.com:aliasproject/ingot.git
cd ingot
go build -o ingot ./cmd    # build the binary
go run ./cmd status        # or run directly without building
go vet ./...               # static checks
```

There's no test suite — `go build`/`go vet` are the verification available.
See [`CLAUDE.md`](CLAUDE.md) for an architecture overview if you're working
on the CLI itself.

## Troubleshooting

**`Error: Docker is not installed or not running`** — start Docker first.

**SSL warnings in the browser** — `ingot cert verify`, then `ingot cert
repair` if the CA isn't installed.

**`ingot install` seems stuck partway through** — just run it again; every
step is safe to repeat and picks up from where it stopped.

**Container pull denied** — re-authenticate with `ingot github-auth`, or if
you're on a fresh `gh` CLI login, make sure it has `read:packages` scope
(`gh auth refresh -h github.com -s read:packages`).
