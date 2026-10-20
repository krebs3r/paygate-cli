// Package cli implements the paygate command tree.
package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/tui"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

// BuildInfo is injected by main via ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// App carries global flags and lazily loaded state for all commands.
type App struct {
	Build       BuildInfo
	ProfileName string
	Output      string
	Verbose     int
	Yes         bool
	NoColor     bool
	Curl        bool
	ShowSecrets bool

	cfg *config.Config
}

// exitError ends the program with a code after output was already rendered.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

var errSilentFail = exitError{code: 1}

// JSON reports whether machine-readable output was requested.
func (a *App) JSON() bool { return a.Output == "json" }

// Config loads the configuration once.
func (a *App) Config() (*config.Config, error) {
	if a.cfg != nil {
		return a.cfg, nil
	}
	c, err := config.Load()
	if err != nil {
		return nil, err
	}
	a.cfg = c
	return c, nil
}

// Profile resolves the active profile.
func (a *App) Profile() (string, *config.Profile, error) {
	c, err := a.Config()
	if err != nil {
		return "", nil, err
	}
	return c.Resolve(a.ProfileName)
}

// Client builds an API client for the active profile.
func (a *App) Client() (string, *config.Profile, *paygate.Client, error) {
	name, p, err := a.Profile()
	if err != nil {
		return "", nil, nil, err
	}
	key, err := config.APIKey(name, p)
	if err != nil {
		return "", nil, nil, err
	}
	c, err := a.newClient(name, p, key)
	return name, p, c, err
}

// Origin returns the API origin of a profile; PAYGATE_BASE_URL overrides it
// (mock server, proxies).
func (a *App) Origin(p *config.Profile) (string, error) {
	return core.Origin(p)
}

func (a *App) newClient(name string, p *config.Profile, apiKey string) (*paygate.Client, error) {
	opts := core.ClientOptions{Version: a.Build.Version, Profile: name}
	if a.Verbose > 0 {
		opts.Transport = &paygate.LoggingTransport{
			Level: a.Verbose,
			Out:   ui.Stderr,
			Style: func(kind, s string) string {
				switch kind {
				case "out":
					return ui.Accent.Render(s)
				case "in":
					return ui.Link.Render(s)
				default:
					return ui.Muted.Render(s)
				}
			},
		}
	}
	return core.NewClient(p, apiKey, opts)
}

// PrintHeader shows the banner in human mode.
func (a *App) PrintHeader(name string, p *config.Profile) {
	if a.JSON() {
		return
	}
	brand := "nexi"
	if p.Brand != "" {
		brand = p.Brand
	}
	ui.Println()
	ui.Println(ui.Header(brand, name, p.MerchantID, p.IsProd()))
	ui.Println()
}

// ConfirmProd asks before anything is sent to production.
func (a *App) ConfirmProd(p *config.Profile, what string) error {
	if !p.IsProd() || a.Yes || a.Curl {
		return nil
	}
	if !ui.Interactive() {
		return errors.New("refusing to " + what + " on PRODUCTION without --yes")
	}
	ok, err := ui.Confirm("This is a PRODUCTION profile", "Really "+what+" with real money?", false)
	if err != nil {
		return err
	}
	if !ok {
		return ui.ErrAborted
	}
	return nil
}

// NewRootCmd builds the full command tree.
func NewRootCmd(build BuildInfo) *cobra.Command {
	app := &App{Build: build}
	ui.Version = build.Version

	root := &cobra.Command{
		Use:   "paygate",
		Short: "Send real Nexi / Computop Paygate payments from your terminal",
		Long: "paygate sends payments to the Nexi / Computop Paygate REST API v2, shows their status\n" +
			"and runs follow-up operations such as capture, refund and cancel.\n\n" +
			"Run paygate without a command to open the full-screen app, or start with\n" +
			"`paygate init` and `paygate pay`.",
		Version:       build.Version,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if app.NoColor || os.Getenv("NO_COLOR") != "" {
				ui.DisableColor()
			}
			switch app.Output {
			case "table", "json":
			default:
				return fmt.Errorf("unknown output format %q (use table or json)", app.Output)
			}
			// The colour theme chosen in the app; errors surface in the command.
			if cfg, err := app.Config(); err == nil {
				ui.SetTheme(cfg.Theme)
			}
			return nil
		},
		// Without a command, paygate opens the full-screen app in a terminal
		// and prints the help otherwise (pipes, CI, -o json).
		RunE: func(cmd *cobra.Command, args []string) error {
			if !ui.Interactive() || app.JSON() {
				return cmd.Help()
			}
			return tui.Run(tui.Options{Version: build.Version, Profile: app.ProfileName})
		},
	}
	root.SetVersionTemplate("paygate {{.Version}}\n")

	f := root.PersistentFlags()
	f.StringVarP(&app.ProfileName, "profile", "p", "", "profile to use (default: active profile, or $PAYGATE_PROFILE)")
	f.StringVarP(&app.Output, "output", "o", "table", "output format: table or json")
	f.CountVarP(&app.Verbose, "verbose", "v", "log HTTP traffic to stderr (-v bodies, -vv headers)")
	f.BoolVarP(&app.Yes, "yes", "y", false, "skip confirmation prompts")
	f.BoolVar(&app.NoColor, "no-color", false, "disable colours (also honours NO_COLOR)")
	f.BoolVar(&app.Curl, "curl", false, "print the request as a curl command instead of sending it")
	f.BoolVar(&app.ShowSecrets, "show-secrets", false, "with --curl: print the API key instead of $PAYGATE_API_KEY")

	root.AddGroup(
		&cobra.Group{ID: "payments", Title: "Payments:"},
		&cobra.Group{ID: "setup", Title: "Setup:"},
	)
	for _, c := range []*cobra.Command{
		newPayCmd(app),
		newGetCmd(app),
		newHistoryCmd(app),
		newOperationCmd(app, opCapture),
		newOperationCmd(app, opRefund),
		newOperationCmd(app, opCancel),
		newTestcardsCmd(app),
	} {
		c.GroupID = "payments"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{
		newInitCmd(app),
		newProfileCmd(app),
		newResetCmd(app),
		newAboutCmd(app),
	} {
		c.GroupID = "setup"
		root.AddCommand(c)
	}
	root.SetHelpCommandGroupID("setup")
	root.SetCompletionCommandGroupID("setup")
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute(build BuildInfo) int {
	root := NewRootCmd(build)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ex exitError
	if errors.As(err, &ex) {
		return ex.code
	}
	if errors.Is(err, ui.ErrAborted) {
		ui.Eprintln(ui.Muted.Render(" Cancelled."))
		return 130
	}
	ui.Eprintln(renderError(err))
	return 1
}

func renderError(err error) string {
	var details []string
	hint := ""
	msg := err.Error()
	switch {
	case errors.Is(err, paygate.ErrUnauthorized):
		hint = "Check merchant ID, API key and auth mode with `paygate profile show`, or run `paygate init` again."
	case errors.Is(err, config.ErrNoProfile):
		msg = "No profile configured yet."
		hint = "Run `paygate init` to add your Paygate merchant credentials."
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "timeout"):
		hint = "Check your internet connection or VPN."
	}
	if e, ok := paygate.AsAPIError(err); ok {
		if e.Problem != nil {
			details = e.Problem.FieldErrors()
			if e.Problem.TraceID != "" {
				details = append(details, ui.Muted.Render("trace "+e.Problem.TraceID))
			}
		}
		if e.Response != nil && e.Response.CorrelationID != "" {
			details = append(details, ui.Muted.Render("correlation "+e.Response.CorrelationID))
		}
		if e.StatusCode == http.StatusNotFound {
			hint = "Check the ID and that the profile (test/prod, brand) matches where the payment was created."
		}
	}
	return ui.ErrorBox(msg, details, hint)
}
