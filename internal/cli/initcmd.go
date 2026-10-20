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
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
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
	if cleaned := config.CleanSecret(o.apiKey); cleaned != o.apiKey {
		o.apiKey = cleaned
		ui.Println(ui.Info("Removed spaces or invisible characters around the pasted API key."))
	}
	if cleaned := config.CleanSecret(o.merchantID); cleaned != o.merchantID {
		o.merchantID = cleaned
		ui.Println(ui.Info("Removed spaces or invisible characters around the merchant ID."))
	}
	if !profileNameRe.MatchString(o.name) {
		return fmt.Errorf("invalid profile name %q (letters, digits, . _ -)", o.name)
	}
	if o.auth != string(paygate.AuthBasic) && o.auth != string(paygate.AuthOAuth2) {
		return fmt.Errorf("unknown auth %q (use basic or oauth2)", o.auth)
	}
	prof := &config.Profile{Brand: o.brand, Env: o.env, MerchantID: o.merchantID, Auth: o.auth}
	if _, err := app.Origin(prof); err != nil {
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
		if err := verifyCredentials(app, o.name, prof, o.apiKey); err != nil {
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
		}
	}
	origin, err := app.Origin(prof)
	if err != nil {
		return err
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
	ui.Println(ui.Muted.Render("   Saved to " + config.Tilde(cfg.FilePath())))
	ui.Println()
	ui.Println(ui.Next("paygate pay", "paygate testcards", "paygate profile list"))
	ui.Println()
	return nil
}

// verifyCredentials checks the credentials. On 401 it tries the other host
// family and auth mode, because Nexi and Computop merchant IDs only work on
// their own hosts and some merchants are set up for OAuth2 only. If nothing
// works it prints what was sent (without the secret) and the usual causes.
func verifyCredentials(app *App, name string, prof *config.Profile, apiKey string) error {
	origin, err := app.Origin(prof)
	if err != nil {
		return err
	}
	client, err := app.newClient(name, prof, apiKey)
	if err != nil {
		return err
	}
	ctx := reqlog.WithoutLog(context.Background())
	err = ui.Spin("Verifying credentials with "+hostOf(origin)+" …", func() error {
		vctx, cancel := context.WithTimeout(ctx, core.ProbeTimeout)
		defer cancel()
		return client.Verify(vctx)
	})
	if err == nil {
		ui.Println(ui.OK("Credentials verified with " + hostOf(origin)))
		return nil
	}
	if !errors.Is(err, paygate.ErrUnauthorized) {
		ui.Eprintln(renderError(err))
		return err
	}

	if os.Getenv("PAYGATE_BASE_URL") == "" {
		var match *config.Profile
		_ = ui.Spin("Checking test/production, Nexi/Computop and Basic/OAuth2 …", func() error {
			match, _ = core.FindWorkingSettings(ctx, prof, func(p *config.Profile) (*paygate.Client, error) {
				return app.newClient(name, p, apiKey)
			})
			return nil
		})
		if match != nil {
			altOrigin, _ := app.Origin(match)
			what := paygate.BrandLabel(paygate.Brand(match.Brand)) + " " + envName(match.Env) + " with " + authLabel(match.Auth)
			ui.Println(ui.Warn("The credentials work with " + ui.Bold.Render(what) + ", not with your selection."))
			if match.IsProd() && !prof.IsProd() {
				ui.Println(ui.Warn(ui.Danger.Render("This is the PRODUCTION system: payments move real money.")))
			}
			use := app.Yes
			if !use && ui.Interactive() {
				if use, err = ui.Confirm("Use "+what+" for this profile?", hostOf(altOrigin), true); err != nil {
					return err
				}
			}
			if use {
				prof.Brand, prof.Env, prof.Auth = match.Brand, match.Env, match.Auth
				ui.Println(ui.OK("Credentials verified with " + hostOf(altOrigin)))
				return nil
			}
		}
	}

	ui.Eprintln(ui.ErrorBox("Paygate rejected the credentials (401 Unauthorized)", []string{
		ui.Label.Render("Host         ") + hostOf(origin),
		ui.Label.Render("Merchant ID  ") + prof.MerchantID,
		ui.Label.Render("API key      ") + fmt.Sprintf("%d characters, ends with %s", len([]rune(apiKey)), keyEnding(apiKey)),
		ui.Label.Render("Auth         ") + authLabel(prof.Auth),
	}, ""))
	ui.Eprintln(ui.Muted.Render("   Common causes:"))
	for _, c := range []string{
		"The key is not the REST API key. Blowfish and HMAC keys do not work here.",
		"The test system is not enabled for this merchant ID (ask Paygate Support).",
		"Merchant ID and key were mistyped, or the key was rotated in the portal.",
	} {
		ui.Eprintln("   " + ui.Accent.Render("•") + " " + ui.Label.Render(c))
	}
	ui.Eprintln()
	return err
}

func envName(env string) string {
	if env == string(paygate.EnvProd) {
		return "production"
	}
	return "test"
}

func keyEnding(k string) string {
	r := []rune(k)
	if len(r) <= 4 {
		return "…"
	}
	return "…" + string(r[len(r)-4:])
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
				huh.NewOption("Production system", "prod"),
			).Value(&o.env).Description(o.apiTarget()).DescriptionFunc(o.apiTarget, []*string{&o.brand, &o.env}),
		),
		huh.NewGroup(
			huh.NewInput().Title("Merchant ID").Value(&o.merchantID).Validate(nonEmpty("merchant ID")),
			huh.NewInput().Title("REST API key").EchoMode(huh.EchoModePassword).Value(&o.apiKey).Validate(nonEmpty("API key")),
			huh.NewSelect[string]().Title("Authentication").Options(
				huh.NewOption("Basic Auth (merchant ID + API key)", "basic"),
				huh.NewOption("OAuth2 client credentials", "oauth2"),
			).Value(&o.auth).Description(o.authTarget()).DescriptionFunc(o.authTarget, []*string{&o.brand, &o.env, &o.auth}),
		),
	)
}

// apiTarget and authTarget say where the calls go with the choice in the
// wizard, while it is made.
func (o *initOptions) apiTarget() string {
	api, _, err := core.Endpoints(&config.Profile{Brand: o.brand, Env: o.env})
	if err != nil {
		return ""
	}
	return "API " + api
}

func (o *initOptions) authTarget() string {
	if o.auth != string(paygate.AuthOAuth2) {
		return "Merchant ID and API key with every request"
	}
	_, token, err := core.Endpoints(&config.Profile{Brand: o.brand, Env: o.env})
	if err != nil {
		return ""
	}
	return "Token from " + token
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
