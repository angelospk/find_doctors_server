package main

import (
	"strings"
	"testing"
)

func TestDefaultListenAddrsAreLoopback(t *testing.T) {
	t.Setenv("LISTEN_ADDR", "")
	t.Setenv("ADMIN_ADDR", "")
	public, admin := listenAddrs()
	for name, addr := range map[string]string{"public": public, "admin": admin} {
		if !strings.HasPrefix(addr, "127.0.0.1:") {
			t.Errorf("%s default %q must bind loopback; set LISTEN_ADDR/ADMIN_ADDR to expose", name, addr)
		}
	}
}
