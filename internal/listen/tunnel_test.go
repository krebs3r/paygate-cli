//go:build !windows

package listen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCloudflared writes a script that prints what cloudflared prints and
// then keeps running, or fails.
func fakeCloudflared(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cloudflared")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTunnelReadsTheAddress(t *testing.T) {
	bin := fakeCloudflared(t, `
echo "2026-10-23T11:45:28Z INF Requesting new quick Tunnel on trycloudflare.com..." >&2
echo "2026-10-23T11:45:34Z INF |  Your quick Tunnel has been created! Visit it at:  |" >&2
echo "2026-10-23T11:45:34Z INF |  https://deputy-kerry-flush-drill.trycloudflare.com  |" >&2
sleep 0.2
echo "2026-10-23T11:45:35Z INF Registered tunnel connection connIndex=0 location=txl01 protocol=quic" >&2
exec sleep 30
`)
	tun, err := StartTunnel(context.Background(), bin, "http://127.0.0.1:1", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if tun.URL != "https://deputy-kerry-flush-drill.trycloudflare.com" {
		t.Errorf("URL = %q", tun.URL)
	}
	start := time.Now()
	tun.Close()
	if time.Since(start) > 2*time.Second {
		t.Errorf("Close took %s", time.Since(start))
	}
}

func TestTunnelReportsFailure(t *testing.T) {
	bin := fakeCloudflared(t, `echo "ERR failed to request quick Tunnel: 429 Too Many Requests" >&2; exit 1`)
	_, err := StartTunnel(context.Background(), bin, "http://127.0.0.1:1", 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "429 Too Many Requests") {
		t.Errorf("error should carry cloudflared's message: %v", err)
	}

	slow := fakeCloudflared(t, "exec sleep 30\n")
	_, err = StartTunnel(context.Background(), slow, "http://127.0.0.1:1", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no address") {
		t.Errorf("timeout: %v", err)
	}

	unregistered := fakeCloudflared(t, `echo "INF |  https://a-b-c-d.trycloudflare.com  |" >&2; exec sleep 30`)
	_, err = StartTunnel(context.Background(), unregistered, "http://127.0.0.1:1", 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "was not registered") {
		t.Errorf("an address without a registered connection is not ready: %v", err)
	}
}

func TestCloudflaredLookup(t *testing.T) {
	bin := fakeCloudflared(t, "exit 0\n")
	t.Setenv("PAYGATE_CLOUDFLARED", "")
	t.Setenv("PATH", filepath.Dir(bin))
	if p, err := Cloudflared(); err != nil || p != bin {
		t.Errorf("Cloudflared() = %q %v", p, err)
	}
	t.Setenv("PAYGATE_CLOUDFLARED", bin+".missing")
	if _, err := Cloudflared(); err != ErrNoCloudflared {
		t.Errorf("a missing $PAYGATE_CLOUDFLARED must count as not installed: %v", err)
	}
}
