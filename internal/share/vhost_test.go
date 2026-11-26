package share

import (
	"strings"
	"testing"

	"github.com/aliasproject/ingot/internal/ssl"
)

func TestShareVhostImportsEmpty500InsideReverseProxy(t *testing.T) {
	vhost := shareVhost("shop", "shop.example.com", ModeNamed, "")
	proxy := strings.Index(vhost, "reverse_proxy ingot_php_shop:80 {")
	imp := strings.Index(vhost, "        "+ssl.Empty500Import+"shop\n    }\n")
	wake := strings.Index(vhost, "    import ingot_wake shop\n")
	if proxy < 0 || imp < proxy || wake < imp {
		t.Fatalf("empty-500 import missing or outside reverse_proxy:\n%s", vhost)
	}
}
