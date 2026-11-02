package core

import (
	"fmt"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/listen"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
)

// RenameResult is what moved with a renamed profile.
type RenameResult struct {
	Payments, Webhooks, Calls int
}

// RenameProfile gives a profile a new name. Its keys, the active profile
// setting and the payments, webhooks and API calls stored for it move along.
// The keys are copied before the config is saved and removed under the old
// name only after, so a failure never loses them.
func RenameProfile(cfg *config.Config, from, to string) (RenameResult, error) {
	var res RenameResult
	p, ok := cfg.Profiles[from]
	if !ok {
		return res, fmt.Errorf("profile %q not found", from)
	}
	if to == from {
		return res, nil
	}
	if !config.ValidName(to) {
		return res, fmt.Errorf("%q is not a valid profile name (letters, digits, . _ -, max. 40)", to)
	}
	if _, exists := cfg.Profiles[to]; exists {
		return res, fmt.Errorf("a profile named %q exists", to)
	}
	if st, ok := listen.For(from); ok {
		return res, fmt.Errorf("paygate listen runs for %s (process %d); stop it first", from, st.PID)
	}
	if err := config.CopySecrets(from, to, p); err != nil {
		return res, err
	}
	active := cfg.ActiveProfile
	cfg.Profiles[to] = p
	delete(cfg.Profiles, from)
	if active == from {
		cfg.ActiveProfile = to
	}
	if err := cfg.Save(); err != nil {
		cfg.Profiles[from] = p
		delete(cfg.Profiles, to)
		cfg.ActiveProfile = active
		config.DeleteAPIKey(to)
		config.DeleteHMACKey(to, nil)
		return res, err
	}
	config.DeleteAPIKey(from)
	config.DeleteHMACKey(from, nil)

	var err error
	if res.Payments, err = history.RenameProfile(from, to); err != nil {
		return res, fmt.Errorf("move the stored payments: %w", err)
	}
	if res.Webhooks, err = listen.RenameProfile(from, to); err != nil {
		return res, fmt.Errorf("move the stored webhooks: %w", err)
	}
	if res.Calls, err = reqlog.RenameProfile(from, to); err != nil {
		return res, fmt.Errorf("move the stored API calls: %w", err)
	}
	return res, nil
}
