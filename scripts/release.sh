#!/bin/bash

# Release script for Alios CLI: bumps the version, builds binaries for every
# platform, then commits + tags the bump and publishes a GitHub release with
# the binaries attached. Releases are distributed via GitHub Releases
# directly (internal/updater checks the GitHub API) -- there's no separate
# manifest/cloud-storage upload step.
set -e

# Configuration
VERSION="${1:-}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# Check version argument
if [ -z "$VERSION" ]; then
    echo -e "${RED}Error: Version required${NC}"
    echo "Usage: $0 <version> [notes]"
    echo "Example: $0 1.0.0 'Bug fixes and improvements'"
    exit 1
fi

NOTES="${2:-Bug fixes and improvements}"

REPO_ROOT="$(cd .. && pwd)"

# A dirty tree would otherwise get swept into the version-bump commit below.
if [ -n "$(git -C "$REPO_ROOT" status --porcelain)" ]; then
    echo -e "${RED}Error: working tree is not clean -- commit or stash first${NC}"
    exit 1
fi

echo -e "${GREEN}Building Alios v${VERSION}${NC}"

# Update version in code. Anchored to a line starting with "Version" (after
# leading whitespace) specifically so it doesn't also match DefaultPHPVersion
# = "..." a few lines down - a previous unanchored pattern here did exactly
# that, silently overwriting the default PHP version with the release number.
echo "Updating version..."
sed -i.bak -E "s/^([[:space:]]*)Version = \".*\"/\1Version = \"${VERSION}\"/" ../internal/config/config.go
rm ../internal/config/config.go.bak

git -C "$REPO_ROOT" add internal/config/config.go
git -C "$REPO_ROOT" commit -m "Bump version to ${VERSION}"

# Build directory
BUILD_DIR="./build"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Build for each platform
echo -e "${YELLOW}Building binaries...${NC}"

build_binary() {
    local GOOS=$1
    local GOARCH=$2
    local OUTPUT="alios-${GOOS}-${GOARCH}"

    if [ "$GOOS" = "windows" ]; then
        OUTPUT="${OUTPUT}.exe"
    fi

    echo "  → ${GOOS}/${GOARCH}"

    GOOS=$GOOS GOARCH=$GOARCH go build \
        -ldflags="-s -w -X github.com/aliasproject/alios-cli/internal/config.Version=${VERSION}" \
        -o "$BUILD_DIR/$OUTPUT" \
        ../cmd/main.go

    local SIZE=$(stat -f%z "$BUILD_DIR/$OUTPUT" 2>/dev/null || stat -c%s "$BUILD_DIR/$OUTPUT")
    echo "    Size: $(echo $SIZE | awk '{printf "%.1fMB", $1/1024/1024}')"
}

# Build all platforms
build_binary "darwin" "amd64"
build_binary "darwin" "arm64"
build_binary "linux" "amd64"
build_binary "linux" "arm64"
build_binary "windows" "amd64"

echo -e "${GREEN}✓ Build complete!${NC}"

# The alios_idle service image. The CLI runs the tag matching its own
# version (config.IdleImage), so it must be pushed before the release is
# published. Needs `docker buildx` and a ghcr.io login with write:packages.
echo -e "${YELLOW}Building and pushing ghcr.io/aliasproject/alios-idle:${VERSION}...${NC}"
docker buildx build \
    --platform linux/amd64,linux/arm64 \
    -f "$REPO_ROOT/Dockerfile.idle" \
    -t "ghcr.io/aliasproject/alios-idle:${VERSION}" \
    --push \
    "$REPO_ROOT"
echo ""
echo "Files in $BUILD_DIR:"
ls -lh "$BUILD_DIR"

echo ""
echo -e "${YELLOW}Publishing to GitHub...${NC}"

git -C "$REPO_ROOT" push origin HEAD
git -C "$REPO_ROOT" tag "v${VERSION}"
git -C "$REPO_ROOT" push origin "v${VERSION}"

gh release create "v${VERSION}" \
    "$BUILD_DIR"/alios-darwin-amd64 \
    "$BUILD_DIR"/alios-darwin-arm64 \
    "$BUILD_DIR"/alios-linux-amd64 \
    "$BUILD_DIR"/alios-linux-arm64 \
    "$BUILD_DIR"/alios-windows-amd64.exe \
    --repo aliasproject/alios-cli \
    --title "v${VERSION}" \
    --notes "$NOTES"

echo ""
echo -e "${GREEN}✓ Released v${VERSION}: https://github.com/aliasproject/alios-cli/releases/tag/v${VERSION}${NC}"
