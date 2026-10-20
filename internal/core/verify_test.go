package core

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/mockpaygate"
	"github.com/krebs3r/paygate-cli/internal/paygate"
)

func TestFindWorkingSettingsPicksClosestMatch(t *testing.T) {
	m := mockpaygate.New("shop", "key")
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()

	// Only Nexi production accepts the key, with either auth mode.
	factory := func(p *config.Profile) (*paygate.Client, error) {
		key := "wrong"
		if p.Brand == "nexi" && p.Env == "prod" {
			key = "key"
		}
		return paygate.New(srv.URL, "shop", key, paygate.AuthMode(p.Auth)), nil
	}
	selected := &config.Profile{Brand: "nexi", Env: "test", Auth: "basic", MerchantID: "shop"}
	got, ok := FindWorkingSettings(context.Background(), selected, factory)
	if !ok {
		t.Fatal("expected a working combination")
	}
	if got.Brand != "nexi" || got.Env != "prod" || got.Auth != "basic" {
		t.Errorf("got %s/%s/%s, want the closest match nexi/prod/basic", got.Brand, got.Env, got.Auth)
	}
	if selected.Env != "test" {
		t.Error("the selected profile must not be modified")
	}

	none := func(p *config.Profile) (*paygate.Client, error) {
		return paygate.New(srv.URL, "shop", "wrong", paygate.AuthMode(p.Auth)), nil
	}
	if _, ok := FindWorkingSettings(context.Background(), selected, none); ok {
		t.Error("no combination should match")
	}
}
