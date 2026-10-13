package share

import "testing"

func TestQuickTunnelURLSkipsAPIHost(t *testing.T) {
	offline := `ERR Error requesting quick Tunnel error="failed to request quick Tunnel: Post \"https://api.trycloudflare.com/tunnel\": dial tcp: lookup api.trycloudflare.com: no such host"`
	if got := quickTunnelURL(offline); got != "" {
		t.Fatalf("offline log gave URL %q, want none", got)
	}
	ok := "INF |  https://brave-otter-lamp-sun.trycloudflare.com  |"
	if got := quickTunnelURL(ok); got != "https://brave-otter-lamp-sun.trycloudflare.com" {
		t.Fatalf("got %q", got)
	}
}
