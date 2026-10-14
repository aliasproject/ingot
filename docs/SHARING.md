# Sharing local sites (`ingot share`)

`ingot share` gives the current project a public HTTPS URL through a Cloudflare Tunnel, so clients, teammates and your own phone can reach it without touching DNS, certificates or your router.

```bash
ingot share                       # public URL for the current project
ingot share --auth demo:secret    # same, behind HTTP basic auth
ingot share list                  # every active share
ingot share stop                  # stop sharing the current project
```

A share is also ended automatically by `ingot stop`, `ingot remove`, `ingot pause`, `ingot global stop` and `ingot uninstall`. Shares are not restored by `ingot resume`. `ingot restart` and `ingot global restart` keep the share and its URL.

> **Anyone with the URL can reach your dev site**, including debug pages and error dumps. Use `--auth` for anything that isn't throwaway.

## How it works

```
browser ──https──▶ Cloudflare edge ──tunnel──▶ cloudflared container
        ──http──▶ ingot_proxy (Caddy) ──▶ ingot_php_<project>:80
```

- `cloudflared` runs in a container on the `ingot_internal` network and forwards traffic to Caddy.
- Each share is a single vhost file, `<project>.share.caddyfile`, in the `ingot_vhosts` volume. It matches the public hostname exactly.
- That file is the only record of a share. `status`, `list`, `stop` and `doctor` all read it back.
- The app receives the public host in `Host` / `X-Forwarded-Host`, plus `X-Forwarded-Proto: https`.
- The visitor's IP arrives in `X-Real-IP` / `X-Forwarded-For`, taken from Cloudflare's `Cf-Connecting-Ip` header.
- Every shared request also carries `X-Ingot-Share: 1`. Caddy sets it itself, so visitors can't forge or strip it. An image or app can check it to tell shared requests apart from local ones.

## Quick tunnels (default)

No account or setup is needed. Each share runs its own `ingot_share_<project>` container and gets a random `https://<words>.trycloudflare.com` URL.

- The URL changes every time you share.
- Cloudflare offers quick tunnels with no uptime guarantee.
- Quick tunnels allow about 200 concurrent requests and don't support Server-Sent Events.

If the tunnel container dies (for example after a reboot), the share is shown as `[STALE]` in `ingot status` and `ingot doctor`. Run `ingot share stop`, then `ingot share` again.

## Named tunnels (stable URLs)

Set both of these to give every project a stable `https://<project>.<domain>` URL:

```bash
export INGOT_SHARE_TUNNEL_TOKEN=eyJh...     # tunnel token from the Cloudflare dashboard
export INGOT_SHARE_DOMAIN=acme-dev.com
```

**One-time setup in the Cloudflare dashboard** (Zero Trust → Networks → Tunnels):

1. Create a tunnel (cloudflared connector) and copy its token.
2. Add a **public hostname** `*.acme-dev.com` with service `HTTP` → `ingot_proxy:80`.
3. Make sure a proxied wildcard DNS record `*` → `<tunnel-id>.cfargotunnel.com` exists in the zone. The dashboard normally creates it.

How it runs:

- All named shares share one `ingot_share` container.
- That container is removed when the last named share stops. Leaving it running would bounce hosts that aren't shared through Caddy's http→https redirect in a loop.
- Project names are turned into a single DNS label, e.g. `michaelaguiar.dev` → `michaelaguiar-dev.acme-dev.com`.

**Use a zone apex for `INGOT_SHARE_DOMAIN`**, ideally a dedicated domain:

- Cloudflare's free Universal SSL certificate only covers first-level wildcards (`*.acme-dev.com`).
- So `share.acme.com` would need a paid Advanced Certificate for `*.share.acme.com`.
- A dedicated domain also keeps the wildcard record from shadowing subdomains of your real site.

To protect every named share with SSO instead of `--auth`, add a Cloudflare Access application for `*.acme-dev.com`.

## Framework notes

The CLI stays runtime-agnostic: it only sends standard forwarded headers, and never recreates the project container or injects env for sharing. How well an app copes with being served from a second hostname depends on the app:

- **Laravel:** URLs are built from the request host, so shares mostly just work. Trust the proxy (`$middleware->trustProxies(at: '*')`) so `X-Forwarded-Proto` is honoured and links aren't generated as `http://`. Vite dev-server assets (`localhost:5173`) won't load remotely, so run `npm run build` before sharing.
- **WordPress:** not supported yet. `siteurl`/`home` are stored in the database, so WordPress redirects visitors back to `<project>.test`. No fix has been settled on yet.
