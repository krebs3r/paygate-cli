package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newProfileCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profile",
		Aliases: []string{"profiles"},
		Short:   "List, switch and edit profiles",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List all profiles",
			Args:    cobra.NoArgs,
			RunE:    func(cmd *cobra.Command, args []string) error { return profileList(app) },
		},
		&cobra.Command{
			Use:   "use <name>",
			Short: "Switch the active profile",
			Args:  cobra.ExactArgs(1),
			ValidArgsFunction: func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
				return profileNames(app), cobra.ShellCompDirectiveNoFileComp
			},
			RunE: func(cmd *cobra.Command, args []string) error { return profileUse(app, args[0]) },
		},
		&cobra.Command{
			Use:   "show [name]",
			Short: "Show a profile (default: the active one)",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 1 {
					app.ProfileName = args[0]
				}
				return profileShow(app)
			},
		},
		&cobra.Command{
			Use:   "delete <name>",
			Short: "Delete a profile and its stored API key",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return profileDelete(app, args[0]) },
		},
		&cobra.Command{
			Use:   "set <key> <value>",
			Short: "Change a profile setting (currency, language, return-url, cancel-url, webhook-url, auth)",
			Long: `Change a setting of the active profile (or --profile).

Keys: currency, language, return-url, cancel-url, webhook-url, auth.
Use the value "default" to reset a setting.

Paygate only calls HTTPS URLs on port 443, so callback URLs cannot point
to localhost.`,
			Example: "  paygate profile set currency CHF\n  paygate profile set return-url https://shop.example.com/paygate/return",
			Args:    cobra.ExactArgs(2),
			RunE:    func(cmd *cobra.Command, args []string) error { return profileSet(app, args[0], args[1]) },
		},
	)
	return cmd
}

func profileNames(app *App) []string {
	cfg, err := app.Config()
	if err != nil {
		return nil
	}
	return cfg.Names()
}

func profileList(app *App) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	if app.JSON() {
		type row struct {
			Name       string `json:"name"`
			Active     bool   `json:"active"`
			Brand      string `json:"brand"`
			Env        string `json:"env"`
			MerchantID string `json:"merchantId"`
			Auth       string `json:"auth"`
		}
		out := []row{}
		for _, n := range cfg.Names() {
			p := cfg.Profiles[n]
			out = append(out, row{n, n == cfg.ActiveProfile, p.Brand, p.Env, p.MerchantID, p.Auth})
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		ui.Println(string(b))
		return nil
	}
	if len(cfg.Profiles) == 0 {
		return config.ErrNoProfile
	}
	var rows [][]string
	for _, n := range cfg.Names() {
		p := cfg.Profiles[n]
		mark := " "
		if n == cfg.ActiveProfile {
			mark = "●"
		}
		env := "test"
		if p.IsProd() {
			env = "PROD"
		}
		rows = append(rows, []string{mark, n, p.Brand, env, p.MerchantID, authLabel(p.Auth)})
	}
	ui.Println()
	ui.Println(ui.Table([]string{"", "Profile", "Platform", "Env", "Merchant ID", "Auth"}, rows))
	ui.Println()
	ui.Println(ui.Next("paygate profile use <name>", "paygate init"))
	ui.Println()
	return nil
}

func profileUse(app *App, name string) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found (available: %s)", name, strings.Join(cfg.Names(), ", "))
	}
	cfg.ActiveProfile = name
	if err := cfg.Save(); err != nil {
		return err
	}
	ui.Println(ui.OK("Active profile: " + ui.Bold.Render(name)))
	return nil
}

func profileShow(app *App) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	name, p, err := cfg.Resolve(app.ProfileName)
	if err != nil {
		return err
	}
	origin, _ := app.Origin(p)
	keyInfo := ui.Danger.Render("missing")
	if k, err := config.APIKey(name, p); err == nil {
		where := "system keychain"
		if p.SecretStore == config.StoreFile {
			where = "config file"
		}
		keyInfo = maskKey(k) + ui.Muted.Render("  in "+where)
	}
	if app.JSON() {
		b, _ := json.MarshalIndent(map[string]any{
			"name": name, "active": name == cfg.ActiveProfile, "brand": p.Brand, "env": p.Env,
			"host": origin, "merchantId": p.MerchantID, "auth": p.Auth,
			"currency": p.DefaultCurrency(), "language": p.DefaultLanguage(),
			"returnUrl": p.Return(), "cancelUrl": p.Cancel(), "webhookUrl": p.Webhook(),
		}, "", "  ")
		ui.Println(string(b))
		return nil
	}
	badge := ui.Badge("PROFILE", ui.ToneAccent)
	if name == cfg.ActiveProfile {
		badge = ui.Badge("● ACTIVE", ui.ToneSuccess)
	}
	ui.Println()
	ui.Println(ui.Card{
		Badge: badge,
		Title: name,
		Rows: []ui.Row{
			{Label: "Platform", Value: paygate.BrandLabel(paygate.Brand(p.Brand))},
			{Label: "Environment", Value: ui.EnvBadge(p.IsProd())},
			{Label: "Host", Value: hostOf(origin)},
			{Label: "Merchant ID", Value: p.MerchantID},
			{Label: "Auth", Value: authLabel(p.Auth)},
			{Label: "API key", Value: keyInfo},
			{Label: "Currency", Value: p.DefaultCurrency()},
			{Label: "Language", Value: p.DefaultLanguage()},
			{Label: "Return URL", Value: urlValue(p.ReturnURL, p.Return())},
			{Label: "Cancel URL", Value: urlValue(p.CancelURL, p.Cancel())},
			{Label: "Webhook URL", Value: urlValue(p.WebhookURL, p.Webhook())},
		},
	}.String())
	ui.Println()
	return nil
}

func urlValue(set, effective string) string {
	if set == "" {
		return effective + ui.Muted.Render("  (default)")
	}
	return effective
}

func profileDelete(app *App, name string) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	if !app.Yes {
		if !ui.Interactive() {
			return fmt.Errorf("refusing to delete profile %q without --yes", name)
		}
		ok, err := ui.Confirm("Delete profile "+name+"?", "The stored API key is removed as well.", false)
		if err != nil {
			return err
		}
		if !ok {
			return ui.ErrAborted
		}
	}
	delete(cfg.Profiles, name)
	config.DeleteAPIKey(name)
	if cfg.ActiveProfile == name {
		cfg.ActiveProfile = ""
		if names := cfg.Names(); len(names) > 0 {
			cfg.ActiveProfile = names[0]
		}
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	ui.Println(ui.OK("Deleted profile " + name))
	if cfg.ActiveProfile != "" {
		ui.Println(ui.Info("Active profile: " + cfg.ActiveProfile))
	}
	return nil
}

func profileSet(app *App, key, value string) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	name, p, err := cfg.Resolve(app.ProfileName)
	if err != nil {
		return err
	}
	v := strings.TrimSpace(value)
	if strings.EqualFold(v, "default") {
		v = ""
	}
	checkURL := func(u string) error {
		if u != "" && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("%s must be an https:// URL (Paygate only calls port 443)", key)
		}
		return nil
	}
	switch strings.ToLower(key) {
	case "currency":
		p.Currency = strings.ToUpper(v)
	case "language":
		p.Language = strings.ToLower(v)
	case "return-url":
		if err := checkURL(v); err != nil {
			return err
		}
		p.ReturnURL = v
	case "cancel-url":
		if err := checkURL(v); err != nil {
			return err
		}
		p.CancelURL = v
	case "webhook-url":
		if err := checkURL(v); err != nil {
			return err
		}
		p.WebhookURL = v
	case "auth":
		if v != string(paygate.AuthBasic) && v != string(paygate.AuthOAuth2) {
			return fmt.Errorf("auth must be basic or oauth2")
		}
		p.Auth = v
	default:
		return fmt.Errorf("unknown setting %q (currency, language, return-url, cancel-url, webhook-url, auth)", key)
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	shown := v
	if shown == "" {
		shown = "default"
	}
	ui.Println(ui.OK(fmt.Sprintf("%s · %s = %s", name, key, shown)))
	return nil
}
