# Devbox Update System

A minimal, efficient auto-update system for distributing Devbox binaries.

## Overview

The update system uses a simple architecture:
- **One JSON manifest** hosted in cloud storage
- **Direct binary downloads** with SHA256 verification
- **Atomic replacement** with automatic rollback on failure

## Setup Guide

### 1. Choose Your Storage Provider

#### Option A: AWS S3 (Most Common)
```bash
# Create bucket
aws s3 mb s3://devbox-updates

# Make bucket public (for free downloads)
aws s3api put-bucket-policy --bucket devbox-updates --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Sid": "PublicRead",
    "Effect": "Allow",
    "Principal": "*",
    "Action": "s3:GetObject",
    "Resource": "arn:aws:s3:::devbox-updates/*"
  }]
}'
```

#### Option B: Cloudflare R2 (Best Value - No Egress Fees)
```bash
# Install wrangler
npm install -g wrangler

# Create bucket
wrangler r2 bucket create devbox-updates

# Enable public access via R2.dev subdomain
wrangler r2 bucket update devbox-updates --public
```

#### Option C: Google Cloud Storage
```bash
# Create bucket
gsutil mb gs://devbox-updates

# Make public
gsutil iam ch allUsers:objectViewer gs://devbox-updates
```

### 2. Build and Release

```bash
# Navigate to scripts directory
cd scripts

# Build release (updates version in code automatically)
./release.sh 1.0.0 "Initial release"

# Upload to your storage provider
# The script will show the exact commands to run
```

### 3. Configure Update URL

Set the manifest URL in your environment:

```bash
# AWS S3
export DEVBOX_UPDATE_URL="https://devbox-updates.s3.amazonaws.com/stable/manifest.json"

# Cloudflare R2
export DEVBOX_UPDATE_URL="https://your-account.r2.dev/stable/manifest.json"

# Custom domain
export DEVBOX_UPDATE_URL="https://updates.yourdomain.com/stable/manifest.json"
```

## Manifest Structure

The `manifest.json` file contains all update information:

```json
{
  "version": "1.0.0",
  "date": "2024-01-20",
  "notes": "Bug fixes and improvements",
  "critical": false,
  "downloads": {
    "darwin_amd64": {
      "url": "https://bucket.s3.amazonaws.com/stable/1.0.0/devbox-darwin-amd64",
      "sha256": "abc123...",
      "size": 15728640
    },
    "darwin_arm64": {
      "url": "https://bucket.s3.amazonaws.com/stable/1.0.0/devbox-darwin-arm64",
      "sha256": "def456...",
      "size": 15728640
    },
    "linux_amd64": {
      "url": "https://bucket.s3.amazonaws.com/stable/1.0.0/devbox-linux-amd64",
      "sha256": "ghi789...",
      "size": 15728640
    }
  }
}
```

## User Commands

Users can update with a simple command:

```bash
# Check for updates and install if available
devbox update

# Force reinstall current version
devbox update --force
```

## Security Features

1. **SHA256 Verification**: Every download is verified against its checksum
2. **Atomic Replacement**: Binary is replaced atomically to prevent corruption
3. **Automatic Rollback**: If update fails, the previous version is restored
4. **HTTPS Only**: All downloads occur over secure connections

## Cost Optimization

### Cloudflare R2 (Recommended)
- **$0.015/GB/month** storage
- **$0 egress fees** (huge savings!)
- Fast global CDN included

### AWS S3
- **$0.023/GB/month** storage
- **$0.09/GB** egress (can add up quickly)
- Consider CloudFront for better performance

### Cache Settings
- Manifest: 5 minutes (allows quick update detection)
- Binaries: Forever (versioned URLs never change)

## Testing Updates Locally

```bash
# Start local server in build directory
cd build
python3 -m http.server 8080

# In another terminal, set update URL
export DEVBOX_UPDATE_URL="http://localhost:8080/manifest.json"

# Test update
devbox update
```

## Monitoring

Track update adoption by monitoring:
- Storage bucket access logs
- Download counts per version
- Geographic distribution of downloads

## Rollback Plan

If a bad release is deployed:

1. **Quick Fix**: Update manifest.json to point to previous version
2. **Full Rollback**: Remove bad binaries from storage
3. **Critical Fix**: Set `"critical": true` in manifest for forced updates

## License Key Integration

The update system works independently of license validation:
- Updates are freely available to all users
- License checks happen within the app after update
- No need to gate updates behind authentication

## Troubleshooting

### Update Fails
- Check network connectivity
- Verify manifest URL is accessible
- Check file permissions on binary location

### Checksum Mismatch
- Regenerate and re-upload binaries
- Ensure upload completed successfully
- Verify manifest has correct SHA256 values

### Permission Denied
- Ensure binary location is writable
- On Unix: may need `sudo` for system-wide installs
- On Windows: may need administrator privileges