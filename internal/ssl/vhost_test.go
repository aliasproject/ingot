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
