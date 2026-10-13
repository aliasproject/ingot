package services

import _ "embed"

// defaultPage is served by the reverse proxy's catch-all site for any host
// that has no project vhost (a typo'd domain, or a project that was never
// started) - the equivalent of the branded default server the old nginx
// image shipped. Compiled into the binary and written into the ingot_vhosts
// volume on every global-services start, so it's a static asset, not a
// config file on disk.
//
//go:embed default_page.html
var defaultPage []byte
