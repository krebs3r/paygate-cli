package listen

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ErrNoCloudflared means cloudflared is not installed.
var ErrNoCloudflared = errors.New("cloudflared is not installed")

var tunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// Tunnel is a cloudflared quick tunnel in front of the listener: no account,
// a random trycloudflare.com address that lives as long as the process.
type Tunnel struct {
	URL  string
	cmd  *exec.Cmd
	done chan struct{}

	mu   sync.Mutex
	tail []string
}

// Cloudflared finds the cloudflared program: $PAYGATE_CLOUDFLARED, $PATH,
// then the places Homebrew installs to, which a non-login shell may not
// have on $PATH.
func Cloudflared() (string, error) {
	if p := os.Getenv("PAYGATE_CLOUDFLARED"); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", ErrNoCloudflared
	}
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{"/opt/homebrew/bin/cloudflared", "/usr/local/bin/cloudflared"} {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p, nil
			}
		}
	}
	return "", ErrNoCloudflared
}

// InstallHint is the way to install cloudflared on this system.
func InstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install cloudflared"
	case "windows":
		return "winget install --id Cloudflare.cloudflared"
	default:
		return "https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"
	}
}

// registered is what cloudflared logs once the tunnel takes requests,
// about a second after it prints the address.
const registered = "Registered tunnel connection"

// StartTunnel starts a quick tunnel to localURL and waits until it has an
// address and is registered.
func StartTunnel(ctx context.Context, bin, localURL string, limit time.Duration) (*Tunnel, error) {
	cmd := exec.Command(bin, "tunnel", "--no-autoupdate", "--url", localURL)
	ownGroup(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start cloudflared: %w", err)
	}
	t := &Tunnel{cmd: cmd, done: make(chan struct{})}
	found := make(chan string, 1)
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		isReady := false
		for sc.Scan() {
			line := sc.Text()
			t.remember(line)
			if u := tunnelURL.FindString(line); u != "" {
				select {
				case found <- u:
				default:
				}
			}
			if !isReady && strings.Contains(line, registered) {
				isReady = true
				close(ready)
			}
		}
		_, _ = io.Copy(io.Discard, pr)
	}()
	go func() {
		_ = cmd.Wait()
		pw.Close()
		close(t.done)
	}()

	timeout := time.After(limit)
	for t.URL == "" || ready != nil {
		select {
		case u := <-found:
			t.URL = u
		case <-ready:
			ready = nil
		case <-t.done:
			return nil, fmt.Errorf("cloudflared stopped before the tunnel was ready:\n%s", t.lastLines())
		case <-timeout:
			t.Close()
			if t.URL == "" {
				return nil, fmt.Errorf("cloudflared had no address after %s:\n%s", limit, t.lastLines())
			}
			return nil, fmt.Errorf("the tunnel %s was not registered after %s:\n%s", t.URL, limit, t.lastLines())
		case <-ctx.Done():
			t.Close()
			return nil, ctx.Err()
		}
	}
	return t, nil
}

// WaitResolvable waits until the tunnel's host name resolves, asking a
// public resolver directly. Asking the system too early would leave a
// "no such host" in its cache for minutes. If the public resolver cannot
// be reached, it waits a few seconds instead.
func (t *Tunnel) WaitResolvable(ctx context.Context, limit time.Duration) {
	host := strings.TrimPrefix(t.URL, "https://")
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 2 * time.Second}
			return d.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	deadline := time.Now().Add(limit)
	failures := 0
	for time.Now().Before(deadline) {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		addrs, err := r.LookupHost(lctx, host)
		cancel()
		if err == nil && len(addrs) > 0 {
			return
		}
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
			if failures++; failures >= 3 { // no public DNS here
				sleep(ctx, 3*time.Second)
				return
			}
		}
		sleep(ctx, 500*time.Millisecond)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (t *Tunnel) remember(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tail = append(t.tail, line)
	if len(t.tail) > 8 {
		t.tail = t.tail[len(t.tail)-8:]
	}
}

func (t *Tunnel) lastLines() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.tail, "\n")
}

// Close ends cloudflared, which removes the address.
func (t *Tunnel) Close() {
	select {
	case <-t.done:
		return
	default:
	}
	interrupt(t.cmd)
	select {
	case <-t.done:
	case <-time.After(3 * time.Second):
		kill(t.cmd)
		<-t.done
	}
}
