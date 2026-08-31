#!/bin/bash

# Minimal release script for Alios CLI
set -e

# Configuration
VERSION="${1:-}"
BUCKET="alios-cli-updates"
BUCKET_URL="https://${BUCKET}.s3.amazonaws.com"

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

echo -e "${GREEN}Building Alios v${VERSION}${NC}"

# Update version in code
echo "Updating version..."
sed -i.bak "s/Version = \".*\"/Version = \"${VERSION}\"/" ../internal/config/config.go
rm ../internal/config/config.go.bak

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

    # Calculate SHA256
    local SHA256=$(sha256sum "$BUILD_DIR/$OUTPUT" | awk '{print $1}')
    local SIZE=$(stat -f%z "$BUILD_DIR/$OUTPUT" 2>/dev/null || stat -c%s "$BUILD_DIR/$OUTPUT")

    echo "    Size: $(echo $SIZE | awk '{printf "%.1fMB", $1/1024/1024}')"
    echo "    SHA256: ${SHA256:0:8}..."

    # Store for manifest
    echo "${GOOS}_${GOARCH}|$OUTPUT|$SHA256|$SIZE" >> "$BUILD_DIR/files.txt"
}

# Build all platforms
build_binary "darwin" "amd64"
build_binary "darwin" "arm64"
build_binary "linux" "amd64"
build_binary "linux" "arm64"
build_binary "windows" "amd64"

# Generate manifest.json
echo -e "${YELLOW}Creating manifest...${NC}"

cat > "$BUILD_DIR/manifest.json" <<EOF
{
  "version": "${VERSION}",
  "date": "$(date -u +"%Y-%m-%d")",
  "notes": "${NOTES}",
  "critical": false,
  "downloads": {
EOF

# Add downloads from files.txt
FIRST=true
while IFS='|' read -r platform filename sha256 size; do
    if [ "$FIRST" = true ]; then
        FIRST=false
    else
        echo "," >> "$BUILD_DIR/manifest.json"
    fi

    cat >> "$BUILD_DIR/manifest.json" <<EOF
    "${platform}": {
      "url": "${BUCKET_URL}/stable/${VERSION}/${filename}",
      "sha256": "${sha256}",
      "size": ${size}
    }
EOF
done < "$BUILD_DIR/files.txt"

cat >> "$BUILD_DIR/manifest.json" <<EOF

  }
}
EOF

rm "$BUILD_DIR/files.txt"

echo -e "${GREEN}✓ Build complete!${NC}"
echo ""
echo "Files in $BUILD_DIR:"
ls -lh "$BUILD_DIR"

# Upload to S3
echo ""
echo -e "${YELLOW}Upload to S3:${NC}"
echo ""
echo "1. Upload binaries:"
echo "   aws s3 cp $BUILD_DIR/ s3://${BUCKET}/stable/${VERSION}/ --recursive --exclude manifest.json"
echo ""
echo "2. Upload manifest:"
echo "   aws s3 cp $BUILD_DIR/manifest.json s3://${BUCKET}/stable/ --cache-control 'max-age=300'"
echo ""
echo "3. Make public (if needed):"
echo "   aws s3 cp s3://${BUCKET}/ s3://${BUCKET}/ --recursive --acl public-read"
