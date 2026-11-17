package core

import (
	"errors"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/config"
)

func TestDemoProfileOnlyReachesTheDemoPaygate(t *testing.T) {
	t.Setenv("PAYGATE_BASE_URL", "https://proxy.example")
	demo := &config.Profile{Brand: "nexi", Env: config.EnvDemo}
	if _, err := Origin(demo); !errors.Is(err, ErrDemoNotRunning) {
		t.Fatalf("without a demo Paygate: %v", err)
	}
	SetDemoOrigin("http://127.0.0.1:4711/")
	defer SetDemoOrigin("")
	if o, err := Origin(demo); err != nil || o != "http://127.0.0.1:4711" {
		t.Fatalf("demo origin = %q, %v", o, err)
	}
	if !LocalOrigin(demo) {
		t.Error("the demo Paygate is local: webhooks need no tunnel")
	}

	config.UseDemo(true)
	defer config.UseDemo(false)
	if _, err := Origin(&config.Profile{Brand: "nexi", Env: "test"}); err == nil {
		t.Error("in demo mode a real profile must not reach a real host")
	}
}
