#!/bin/sh
# Generates the Ingot Local CA and per-domain certificates into
# /certificates (the ingot_certs volume). See internal/ssl/manager.go.
#
# Files are generated when missing, so re-running is safe and the CA is
# never replaced. A domain's csr and crt are also regenerated with a new key,
# and its crt is reissued when it no longer verifies against the CA (the CA
# was regenerated) or expires within 30 days. Ingot runs this on every
# `ingot start` (ssl.SetupProjectSSL). With DOMAIN unset only the CA is
# generated.
#
# No path length limit on the CA: Caddy's on-demand issuer for unknown
# hosts (services.baseCaddyfile) signs through an intermediate it creates.

set -e

CERTS=/certificates
CA_CERT="$CERTS/IngotLocalCA.pem"
CA_KEY="$CERTS/IngotLocalCA.key"
# These names and the CN below must match the constants in
# internal/ssl/manager.go (CACertFile, CAKeyFile, CAName).

# The CA browsers and the OS are asked to trust. Valid for ten years, like
# mkcert's, so users don't have to re-trust it every two.
if [ ! -f "$CA_CERT" ]; then
	openssl req -x509 -new -nodes -sha256 -days 3650 \
		-newkey rsa:3072 -keyout "$CA_KEY" -out "$CA_CERT" \
		-subj "/O=Alias Project/OU=Ingot/CN=Ingot Local CA" \
		-addext "basicConstraints=critical,CA:TRUE" \
		-addext "keyUsage=critical,keyCertSign,cRLSign" \
		-addext "subjectKeyIdentifier=hash"
fi

[ -n "$DOMAIN" ] || exit 0

# A new key invalidates any csr and crt left over from the old one: signing
# the stale csr would give a crt that doesn't match the key.
if [ ! -f "$CERTS/$DOMAIN.key" ]; then
	rm -f "$CERTS/$DOMAIN.csr" "$CERTS/$DOMAIN.crt"
	openssl genrsa -out "$CERTS/$DOMAIN.key" 2048
fi

# A reissue rebuilds the csr too, so it picks up this script's subject
# (certs from the old image's csr had an @aliasproject.ssl email address).
crt="$CERTS/$DOMAIN.crt"
if [ -f "$crt" ] && ! { openssl verify -CAfile "$CA_CERT" "$crt" >/dev/null 2>&1 &&
	openssl x509 -in "$crt" -noout -checkend 2592000 >/dev/null; }; then
	rm -f "$crt" "$CERTS/$DOMAIN.csr"
fi

if [ ! -f "$CERTS/$DOMAIN.csr" ]; then
	openssl req -new -key "$CERTS/$DOMAIN.key" -out "$CERTS/$DOMAIN.csr" \
		-subj "/CN=$DOMAIN"
fi

# 825 days is the longest validity Apple platforms accept for a TLS server
# certificate, and they also require the serverAuth extended key usage.
if [ ! -f "$CERTS/$DOMAIN.crt" ]; then
	ext=$(mktemp)
	cat > "$ext" <<EOF
basicConstraints = CA:FALSE
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:$DOMAIN, DNS:*.$DOMAIN
authorityKeyIdentifier = keyid
EOF
	openssl x509 -req -sha256 -days 825 \
		-in "$CERTS/$DOMAIN.csr" -CA "$CA_CERT" -CAkey "$CA_KEY" -CAcreateserial \
		-out "$CERTS/$DOMAIN.crt" -extfile "$ext"
	rm -f "$ext"
fi
