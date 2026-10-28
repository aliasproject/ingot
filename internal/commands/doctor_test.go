package commands

import (
	"context"
	"reflect"
	"runtime"
	"testing"

	"github.com/aliasproject/ingot/internal/utils"
)

func TestParseGetentAhosts(t *testing.T) {
	out := "::1             STREAM ingot-doctor-probe.test\n" +
		"::1             DGRAM  \n" +
		"::1             RAW    \n" +
		"127.0.0.1       STREAM \n" +
		"127.0.0.1       DGRAM  \n" +
		"127.0.0.1       RAW    \n"
	if got, want := parseGetentAhosts(out), []string{"::1", "127.0.0.1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parseGetentAhosts = %v, want %v", got, want)
	}
	if got := parseGetentAhosts(""); len(got) != 0 {
		t.Fatalf("parseGetentAhosts(\"\") = %v, want none", got)
	}
}

// localhost is in /etc/hosts everywhere, so it resolves through getent
// without touching DNS.
func TestLookupSystemUsesGetent(t *testing.T) {
	if runtime.GOOS != "linux" || !utils.CommandExists("getent") {
		t.Skip("needs Linux with getent")
	}
	addrs, err := lookupSystem(context.Background(), "localhost")
	if err != nil || len(addrs) == 0 {
		t.Fatalf("lookupSystem(localhost) = %v, %v", addrs, err)
	}
	if _, err := lookupSystem(context.Background(), "ingot-no-such-host.invalid"); err == nil {
		t.Fatal("lookupSystem of an unknown name succeeded")
	}
}
