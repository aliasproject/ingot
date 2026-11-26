package ssl

import (
	"strings"
	"testing"
)

const shopVhost = "shop.test {\n    reverse_proxy ingot_php_shop:80\n\n    import ingot_wake shop\n}\n"

func TestSwapVhostEnding(t *testing.T) {
	stopped, changed := swapVhostEnding(shopVhost, "shop", true)
	if !changed || !strings.Contains(stopped, "    import ingot_stopped shop\n") || strings.Contains(stopped, "ingot_wake") {
		t.Fatalf("stop: changed=%v\n%s", changed, stopped)
	}
	back, changed := swapVhostEnding(stopped, "shop", false)
	if !changed || back != shopVhost {
		t.Fatalf("start didn't restore the original vhost:\n%s", back)
	}
	if _, changed := swapVhostEnding(shopVhost, "shop", false); changed {
		t.Fatal("waking an already-waking vhost changed it")
	}
}

func TestSwapVhostEndingMatchesWholeLine(t *testing.T) {
	// "sho" is a prefix of "shop": its stop must not touch shop's vhost.
	if out, changed := swapVhostEnding(shopVhost, "sho", true); changed {
		t.Fatalf("prefix project changed another vhost:\n%s", out)
	}
	// A vhost from before scale to zero has neither import; leave it alone.
	old := "shop.test {\n    reverse_proxy ingot_php_shop:80\n}\n"
	if _, changed := swapVhostEnding(old, "shop", true); changed {
		t.Fatal("pre-scale-to-zero vhost was changed")
	}
}

// The empty-500 import has to sit inside reverse_proxy (handle_response is
// one of its subdirectives), and must survive an ingot stop/start round trip.
func TestCaddyVhostImportsEmpty500InsideReverseProxy(t *testing.T) {
	vhost := caddyVhost("shop", "shop.test", false)
	proxy := strings.Index(vhost, "reverse_proxy ingot_php_shop:80 {")
	imp := strings.Index(vhost, "        "+Empty500Import+"shop\n    }\n")
	wake := strings.Index(vhost, "    "+wakeImport+"shop\n")
	if proxy < 0 || imp < proxy || wake < imp {
		t.Fatalf("empty-500 import missing or outside reverse_proxy:\n%s", vhost)
	}

	stopped, changed := swapVhostEnding(vhost, "shop", true)
	if !changed || !strings.Contains(stopped, Empty500Import+"shop\n") {
		t.Fatalf("stop dropped the empty-500 import:\n%s", stopped)
	}
	if back, _ := swapVhostEnding(stopped, "shop", false); back != vhost {
		t.Fatalf("start didn't restore the vhost:\n%s", back)
	}
	if got := caddyVhost("shop", "shop.test", true); got != stopped {
		t.Fatalf("stopped vhost differs from a swapped one:\n%s", got)
	}
}
