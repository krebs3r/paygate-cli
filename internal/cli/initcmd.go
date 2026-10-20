package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

type initOptions struct {
	name        string
	brand       string
	env         string
	merchantID  string
	apiKey      string
	apiKeyStdin bool
	auth        string
	noVerify    bool
}

func newInitCmd(app *App) *cobra.Command {
	o := &initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Add or update a Paygate profile (merchant credentials)",
		Long: `Add or update a profile with your Paygate merchant credentials.

The merchant ID and REST API key come from the Paygate Merchant Portal
(Key management). The API key is stored in your system keychain. paygate
verifies the credentials before saving them.`,
		Example: `  paygate init
  paygate init --name shop-test --brand nexi --env test --merchant-id MyShop --api-key-stdin < key.txt`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(app, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.name, "name", "", "profile name (default: \"default\" for the first profile)")
	f.StringVar(&o.brand, "brand", "nexi", "nexi or computop (same platform, different hosts)")
	f.StringVar(&o.env, "env", "test", "test or prod")
	f.StringVar(&o.merchantID, "merchant-id", "", "Paygate merchant ID")
	f.StringVar(&o.apiKey, "api-key", "", "REST API key (prefer --api-key-stdin or the prompt)")
	f.BoolVar(&o.apiKeyStdin, "api-key-stdin", false, "read the API key from stdin")
	f.StringVar(&o.auth, "auth", "basic", "authentication: basic or oauth2")
	f.BoolVar(&o.noVerify, "no-verify", false, "save without checking the credentials")
	return cmd
}

func runInit(app *App, o *initOptions) error {
	cfg, err := app.Config()
	if err != nil {
		return err
	}
	if o.name == "" && len(cfg.Profiles) == 0 {
		o.name = "default"
	}
	if o.apiKeyStdin {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read API key from stdin: %w", err)
		}
		o.apiKey = strings.TrimSpace(line)
	}
	if o.apiKey == "" {
		o.apiKey = os.Getenv("PAYGATE_API_KEY")
	}

	if o.merchantID == "" || o.apiKey == "" || o.name == "" {
		if !ui.Interactive() {
			return errors.New("missing --name, --merchant-id or --api-key (or run interactively)")
		}
		if err := initWizard(o, cfg); err != nil {
			return err
		}
	}
	o.brand, o.env, o.auth = strings.ToLower(o.brand), strings.ToLower(o.env), strings.ToLower(o.auth)
	if !profileNameRe.MatchString(o.name) {
		return fmt.Errorf("invalid profile name %q (letters, digits, . _ -)", o.name)
	}
	if o.auth != string(paygate.AuthBasic) && o.auth != string(paygate.AuthOAuth2) {
		return fmt.Errorf("unknown auth %q (use basic or oauth2)", o.auth)
	}
	prof := &config.Profile{Brand: o.brand, Env: o.env, MerchantID: strings.TrimSpace(o.merchantID), Auth: o.auth}
	origin, err := app.Origin(prof)
	if err != nil {
		return err
	}
	if existing, ok := cfg.Profiles[o.name]; ok {
		// Keep per-profile settings such as callback URLs.
		prof.Currency, prof.Language = existing.Currency, existing.Language
		prof.ReturnURL, prof.CancelURL, prof.WebhookURL = existing.ReturnURL, existing.CancelURL, existing.WebhookURL
		if !app.Yes && ui.Interactive() {
			ok, err := ui.Confirm("Profile "+o.name+" exists", "Overwrite its credentials?", true)
			if err != nil {
				return err
			}
			if !ok {
				return ui.ErrAborted
			}
		}
	}

	if !o.noVerify {
		client, err := app.newClient(prof, o.apiKey)
		if err != nil {
			return err
		}
		err = ui.Spin("Verifying credentials with "+hostOf(origin)+" …", func() error {
			return client.Verify(context.Background())
		})
		if err != nil {
			ui.Eprintln(renderError(err))
			if !ui.Interactive() {
				return errSilentFail
			}
			save, cerr := ui.Confirm("Credentials could not be verified", "Save the profile anyway?", false)
			if cerr != nil {
				return cerr
			}
			if !save {
				return errSilentFail
			}
		} else {
			ui.Println(ui.OK("Credentials verified with " + hostOf(origin)))
		}
	}

	store, err := config.StoreAPIKey(o.name, prof, o.apiKey)
	if err != nil {
		return err
	}
	cfg.Profiles[o.name] = prof
	cfg.ActiveProfile = o.name
	if err := cfg.Save(); err != nil {
		return err
	}

	storeLabel := "system keychain"
	if store == config.StoreFile {
		storeLabel = "config file (no keychain available)"
	}
	ui.Println()
	ui.Println(ui.Card{
		Badge: ui.Badge("✓ SAVED", ui.ToneSuccess),
		Title: "Profile " + o.name,
		Rows: []ui.Row{
			{Label: "Platform", Value: paygate.BrandLabel(paygate.Brand(o.brand))},
			{Label: "Environment", Value: ui.EnvBadge(prof.IsProd())},
			{Label: "Host", Value: hostOf(origin)},
			{Label: "Merchant ID", Value: prof.MerchantID},
			{Label: "Auth", Value: authLabel(prof.Auth)},
			{Label: "API key", Value: maskKey(o.apiKey) + ui.Muted.Render("  in "+storeLabel)},
		},
	}.String())
	ui.Println(ui.Muted.Render("   Saved to " + tildePath(cfg.FilePath())))
	ui.Println()
	ui.Println(ui.Next("paygate pay", "paygate testcards", "paygate profile list"))
	ui.Println()
	return nil
}

func initWizard(o *initOptions, cfg *config.Config) error {
	if o.name == "" {
		o.name = "default"
		if _, taken := cfg.Profiles["default"]; taken {
			o.name = ""
		}
	}
	return ui.RunForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Connect your Paygate merchant").
				Description("You find merchant ID and REST API key in the Paygate Merchant Portal\nunder Key management. The key is stored in your system keychain."),
			huh.NewInput().Title("Profile name").Placeholder("shop-test").Value(&o.name).Validate(func(s string) error {
				if !profileNameRe.MatchString(s) {
					return errors.New("letters, digits, . _ - (max. 40)")
				}
				return nil
			}),
			huh.NewSelect[string]().Title("Platform").Options(
				huh.NewOption("Nexi Paygate      paygate.nexigroup.com", "nexi"),
				huh.NewOption("Computop Paygate  computop-paygate.com", "computop"),
			).Value(&o.brand),
			huh.NewSelect[string]().Title("Environment").Options(
				huh.NewOption("Test system", "test"),
				huh.NewOption("Production (real money)", "prod"),
			).Value(&o.env),
		),
		huh.NewGroup(
			huh.NewInput().Title("Merchant ID").Value(&o.merchantID).Validate(nonEmpty("merchant ID")),
			huh.NewInput().Title("REST API key").EchoMode(huh.EchoModePassword).Value(&o.apiKey).Validate(nonEmpty("API key")),
			huh.NewSelect[string]().Title("Authentication").Options(
				huh.NewOption("Basic Auth (merchant ID + API key)", "basic"),
				huh.NewOption("OAuth2 client credentials", "oauth2"),
			).Value(&o.auth),
		),
	)
}

func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func nonEmpty(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("enter the " + what)
		}
		return nil
	}
}

func authLabel(a string) string {
	if a == string(paygate.AuthOAuth2) {
		return "OAuth2 client credentials"
	}
	return "Basic Auth"
}

func maskKey(k string) string {
	if len(k) <= 4 {
		return "••••"
	}
	return "••••••••" + k[len(k)-4:]
}
