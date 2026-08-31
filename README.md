# Alios CLI

Alios CLI (`alios`) is a single-binary local development environment manager
for PHP projects. It's the local-dev companion to
[Alios](https://github.com/aliasproject), the cloud infrastructure and
application management platform — local environments are built to mirror
production as closely as possible: the same Caddy reverse proxy, the same
prebuilt-vs-custom-Dockerfile image model, the same config-injection pattern.

No server component, no config files. Every command shells out to Docker on
your machine; all state lives in environment variables or Docker container
labels.

## What you get

- Per-project PHP (FrankenPHP) containers, one per project directory
- A shared Caddy reverse proxy with automatic HTTPS for `*.test` domains
  (self-signed, locally-trusted CA — no browser warnings)
- Shared DNS so `*.test` resolves without touching `/etc/hosts`
- Optional MySQL/PostgreSQL, and MailHog for email testing
- Automatic WordPress detection (extra extensions, hardening, live-reload)
- A choice of prebuilt image, your project's own Dockerfile, or an arbitrary
  pulled image — per project, switchable any time
- `.env` automatically injected into the container, same as production
- Pause/resume everything with one command

## Installation

```bash
curl -L -o alios https://github.com/aliasproject/alios-cli/releases/latest/download/alios
chmod +x alios
./alios
```

On first run, `alios` installs itself to `/usr/local/bin/alios` and walks
you through database configuration and GitHub authentication (needed to pull
private images from GHCR). If it's interrupted partway through, just run
`alios install` again — every step is safe to repeat, so it picks up from
wherever it left off rather than starting over.

### Requirements

- Docker installed and running
- macOS or Linux (Windows via WSL2)
- A GitHub account (for pulling private container images)

## Quick start

```bash
cd /path/to/your/project
alios start
```

On the first `alios start` for a project you'll be asked:

1. **How to build the image** — only if a `Dockerfile` is found in the
   project root: use it, use the prebuilt Alios image, or pull a different
   image entirely. No Dockerfile → straight to prebuilt.
2. **PHP version** — only for the prebuilt path; a custom Dockerfile or
   pulled image brings its own PHP version.
3. **Server root** (defaults to `/app/public`).

None of this is asked again on later starts — it's stored on the project's
container labels. Your project is then available at `https://<name>.test`.

## Commands

### Project lifecycle

```
alios start [version]    Start (or create) the project container
alios stop               Stop the project container
alios restart            Restart the project container
alios remove / rm        Remove the project container
alios status             Show detailed status for this project
alios list / ls          List every project
```

### Changing configuration

```
alios php [version]         Change PHP version (prebuilt projects only)
alios server-root [path]    Change the server root directory
alios image [mode]          Switch image mode without recreating from scratch
```

`alios image` takes `prebuilt`, `dockerfile`, or `pull` (with a second arg
for the image to pull), or run it bare for an interactive prompt. Useful for
going back and forth while testing — e.g. `alios image dockerfile` to try
your production build, `alios image prebuilt` to switch back.

### Logs, exec, and shell

```
alios logs
alios exec composer install
alios shell                # shell as your host user
alios shell --user=root    # shell as root
```

### Global services

DNS, Caddy, MailHog, and any configured databases are shared across every
project:

```
alios global start
alios global stop
alios global restart
alios global status
```

### Pause & resume

Stop everything at once to free up CPU/RAM, then bring back exactly what was
running:

```
alios pause
alios resume
```

### Databases

```
alios database add
alios database remove
alios database list
```

MySQL databases get an empty root password. PostgreSQL databases get user
`alios`, password `alios`, database `alios`.

### SSL

```
alios cert install    # Install the CA into your system trust store
alios cert verify     # Check whether it's installed
alios cert debug      # Detailed certificate/Caddy diagnostics
alios cert repair     # Regenerate vhosts and restart Caddy
```

### Everything else

```
alios status          Full system status (services, network, install)
alios github-auth     (Re-)authenticate with GitHub
alios update          Update to the latest version
alios check-update    Check for an available update
alios uninstall       Uninstall Alios CLI
alios help            Show all commands
```

## Image selection

`alios` mirrors production's own model for how a project's container image
is built:

- **Prebuilt** (default) — `ghcr.io/aliasproject/php:<version>-frankenphp-dev`,
  or the `-wordpress-dev` variant if a WordPress install is detected
  (`wp-config.php`/`wp-load.php` in the project root).
- **Custom Dockerfile** — builds from the project's own `Dockerfile`, the
  same one you'd actually deploy. If it declares build args with no default
  (`ARG SOME_TOKEN`, no `=value`), you're prompted for each one explicitly —
  nothing is silently reused from your GitHub auth or anything else.
- **Custom pull** — pulls a specific image you name.

Switch between them any time with `alios image <mode>` — no need to remove
and recreate the project.

## `.env` handling

If a project has a `.env` file, `alios start` loads it and injects every
variable into the container — the same way production supplies a site's
config as plain environment variables, just sourced from your local file
instead of a database setting. Editing `.env` and restarting picks up the
change automatically (the container recreates, the same way changing PHP
version does — Docker has no way to update a running container's
environment in place, and neither does production).

`SERVER_NAME`, `SERVER_ROOT`, `HTTPS`, and `PORT` are always set after your
`.env` values, so nothing in `.env` can accidentally override what Alios CLI
itself needs.

## Environment variables

Alios CLI has no config file — these are the only settings, read from your
shell environment:

```
GITHUB_TOKEN                 GitHub token for pulling private images
ALIOS_DEFAULT_PHP_VERSION    Default PHP version for new projects
ALIOS_HOST_IP                Override auto-detected host IP
ALIOS_DNS_PORT               DNS listener port (default: 53)
ALIOS_ENABLE_SSL             Enable/disable SSL setup (default: true)
ALIOS_ENABLE_DNS             Enable/disable DNS setup (default: true)
ALIOS_AUTO_START_GLOBAL      Auto-start global services (default: true)
```

## Supported versions

- **PHP:** 8.5, 8.4 (default), 8.3, 8.2, 8.1, 8.0, 7.4
- **MySQL:** 8.0, 5.7
- **PostgreSQL:** 16, 15, 14, 13

## Building from source

```bash
git clone git@github.com:aliasproject/alios-cli.git
cd alios-cli
go build -o alios ./cmd    # build the binary
go run ./cmd status        # or run directly without building
go vet ./...               # static checks
```

There's no test suite — `go build`/`go vet` are the verification available.
See [`CLAUDE.md`](CLAUDE.md) for an architecture overview if you're working
on the CLI itself.

## Troubleshooting

**`Error: Docker is not installed or not running`** — start Docker first.

**SSL warnings in the browser** — `alios cert verify`, then `alios cert
repair` if the CA isn't installed.

**`alios install` seems stuck partway through** — just run it again; every
step is safe to repeat and picks up from where it stopped.

**Container pull denied** — re-authenticate with `alios github-auth`, or if
you're on a fresh `gh` CLI login, make sure it has `read:packages` scope
(`gh auth refresh -h github.com -s read:packages`).
