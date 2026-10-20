// Package core holds logic shared by the command line and the full-screen app.
package core

import (
	"context"
	"sync"
	"time"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
)

// ClientFactory builds an API client for a profile.
type ClientFactory func(p *config.Profile) (*paygate.Client, error)

// ProbeTimeout limits each credential check.
const ProbeTimeout = 12 * time.Second

// FindWorkingSettings tries every platform / environment / auth combination
// other than the selected one in parallel and returns the closest one that
// accepts the credentials. Typical mistakes it catches: a production merchant
// set up as "test", a Computop merchant set up as "Nexi", OAuth2-only keys.
func FindWorkingSettings(ctx context.Context, selected *config.Profile, newClient ClientFactory) (*config.Profile, bool) {
	var candidates []config.Profile
	for _, brand := range []string{string(paygate.BrandNexi), string(paygate.BrandComputop)} {
		for _, env := range []string{string(paygate.EnvTest), string(paygate.EnvProd)} {
			for _, auth := range []string{string(paygate.AuthBasic), string(paygate.AuthOAuth2)} {
				if brand == selected.Brand && env == selected.Env && auth == selected.Auth {
					continue
				}
				alt := *selected
				alt.Brand, alt.Env, alt.Auth = brand, env, auth
				candidates = append(candidates, alt)
			}
		}
	}

	ok := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := newClient(&candidates[i])
			if err != nil {
				return
			}
			pctx, cancel := context.WithTimeout(reqlog.WithoutLog(ctx), ProbeTimeout)
			defer cancel()
			ok[i] = c.Verify(pctx) == nil
		}(i)
	}
	wg.Wait()

	best, bestDist := -1, 99
	for i, c := range candidates {
		if !ok[i] {
			continue
		}
		if d := distance(selected, &c); d < bestDist {
			best, bestDist = i, d
		}
	}
	if best < 0 {
		return nil, false
	}
	return &candidates[best], true
}

func distance(a, b *config.Profile) int {
	d := 0
	if a.Brand != b.Brand {
		d++
	}
	if a.Env != b.Env {
		d++
	}
	if a.Auth != b.Auth {
		d++
	}
	return d
}
