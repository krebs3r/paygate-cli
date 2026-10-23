package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
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
			Use:   "set <key> [value]",
			Short: "Change a profile setting (currency, language, return-url, cancel-url, webhook-url, customer-ip, auth, hmac-key)",
			Long: `Change a setting of the active profile (or --profile).

Keys: currency, language, return-url, cancel-url, webhook-url, customer-ip, auth,
hmac-key. Use the value "default" to reset a setting.

Paygate only calls HTTPS URLs on port 443, so callback URLs cannot point
to localhost.

hmac-key is the key Paygate signs Enhanced webhooks with; paygate webhooks
send and verify use it. Without a value it is asked for (hidden) or read from
stdin, and stored in the system keychain.`,
			Example: "  paygate profile set currency CHF\n" +
				"  paygate profile set return-url https://shop.example.com/paygate/return\n" +
				"  paygate profile set hmac-key\n" +
				"  paygate profile set hmac-key < hmac-key.txt\n" +
				"  paygate profile set hmac-key default   # remove it",
			Args: cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(args) == 1 {
					return profileSet(app, args[0], "", false)
				}
				return profileSet(app, args[0], args[1], true)
			},
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
	hmacInfo := ui.Muted.Render("not set · paygate profile set hmac-key")
	hmacSet := false
	if k, err := config.HMACKey(name, p); err != nil {
		hmacInfo = ui.Danger.Render("missing")
	} else if k != "" {
		hmacSet = true
		where := "system keychain"
		switch {
		case os.Getenv("PAYGATE_HMAC_KEY") != "":
			where = "$PAYGATE_HMAC_KEY"
		case p.HMACStore == config.StoreFile:
			where = "config file"
		}
		hmacInfo = maskKey(k) + ui.Muted.Render("  in "+where)
	}
	if app.JSON() {
		b, _ := json.MarshalIndent(map[string]any{
			"name": name, "active": name == cfg.ActiveProfile, "brand": p.Brand, "env": p.Env,
			"host": origin, "merchantId": p.MerchantID, "auth": p.Auth,
			"currency": p.DefaultCurrency(), "language": p.DefaultLanguage(),
			"returnUrl": p.Return(), "cancelUrl": p.Cancel(), "webhookUrl": p.Webhook(),
			"hmacKeySet": hmacSet,
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
			{Label: "HMAC key", Value: hmacInfo},
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
	config.DeleteHMACKey(name, cfg.Profiles[name])
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

func profileSet(app *App, key, value string, given bool) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	name, p, err := cfg.Resolve(app.ProfileName)
	if err != nil {
		return err
	}
	if strings.EqualFold(key, "hmac-key") {
		return profileSetHMACKey(cfg, name, p, value, given)
	}
	if !given {
		return fmt.Errorf("missing value for %s", key)
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
	case "customer-ip":
		if v != "" && net.ParseIP(v) == nil {
			return fmt.Errorf("%q is not a valid IP address", v)
		}
		p.CustomerIP = v
	case "auth":
		if v != string(paygate.AuthBasic) && v != string(paygate.AuthOAuth2) {
			return fmt.Errorf("auth must be basic or oauth2")
		}
		p.Auth = v
	default:
		return fmt.Errorf("unknown setting %q (currency, language, return-url, cancel-url, webhook-url, customer-ip, auth, hmac-key)", key)
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

// profileSetHMACKey stores or, with "default", removes the webhook HMAC key.
// Without a value it is asked for, or read from stdin in scripts.
func profileSetHMACKey(cfg *config.Config, name string, p *config.Profile, value string, given bool) error {
	if given && (strings.TrimSpace(value) == "" || strings.EqualFold(strings.TrimSpace(value), "default")) {
		config.DeleteHMACKey(name, p)
		if err := cfg.Save(); err != nil {
			return err
		}
		ui.Println(ui.OK("Removed the HMAC key of " + name))
		return nil
	}
	if !given {
		if ui.Interactive() {
			err := ui.RunForm(huh.NewGroup(
				huh.NewInput().Title("HMAC key of " + name).
					Description("The key Paygate signs Enhanced webhooks with.\nIt is stored in your system keychain.").
					EchoMode(huh.EchoModePassword).Value(&value).Validate(nonEmpty("HMAC key")),
			))
			if err != nil {
				return err
			}
		} else {
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("read HMAC key from stdin: %w", err)
			}
			value = line
		}
	}
	key := config.CleanSecret(value)
	if key == "" {
		return errors.New("the HMAC key is empty")
	}
	store, err := config.StoreHMACKey(name, p, key)
	if err != nil {
		return err
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	where := "system keychain"
	if store == config.StoreFile {
		where = "config file (no keychain available)"
	}
	ui.Println(ui.OK("HMAC key " + maskKey(key) + " of " + name + " stored in the " + where))
	ui.Println(ui.Next("paygate webhooks send --to <url> --trans-id <id>", "paygate webhooks verify --request <file>"))
	return nil
}
