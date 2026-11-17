package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/demo"
	"github.com/krebs3r/paygate-cli/internal/tui"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newDemoCmd(app *App) *cobra.Command {
	var reset bool
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Try paygate without credentials, against a demo Paygate on this computer",
		Long: `Open the app in demo mode: paygate starts a demo Paygate on this computer
(127.0.0.1) and uses the profile "demo" with made-up credentials. Every
function works as with a real merchant, but no request leaves this computer,
no credentials are needed and no money moves. Hosted payments end on a demo
checkout page in your browser, where you pay, decline or cancel.

The demo keeps its payments, request log and webhooks in its own folder,
apart from your real profiles; your keychain is not touched. Single commands
run in demo mode with the global flag --demo.

Answers of the demo Paygate follow the Paygate API reference, but they are
made up: what counts is the real Paygate.`,
		Example: `  paygate demo                                   # open the app in demo mode
  paygate --demo pay --amount 49.90 --card visa-ok
  paygate --demo pay --hosted                    # pay on the demo checkout page
  paygate --demo get <transId>
  paygate demo --reset                           # delete the demo's payments and logs`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if reset {
				return runDemoReset(app)
			}
			if !ui.Interactive() || app.JSON() {
				return errors.New("paygate demo opens the app and needs a terminal; for single commands use `paygate --demo <command>`")
			}
			if _, err := demo.Start(); err != nil {
				return err
			}
			defer demo.Stop()
			return tui.Run(tui.Options{Version: app.Build.Version, Commit: app.Build.Commit})
		},
	}
	cmd.Flags().BoolVar(&reset, "reset", false, "delete the demo's payments, request log and webhooks")
	return cmd
}

// runDemoReset deletes the demo folder after asking.
func runDemoReset(app *App) error {
	dir, err := demo.Dir()
	if err != nil {
		return err
	}
	if !app.Yes {
		if !ui.Interactive() {
			return errors.New("refusing to delete the demo data without --yes")
		}
		ok, err := ui.Confirm("Delete the demo data?",
			fmt.Sprintf("Payments, request log and webhooks of the demo in %s. Your real profiles stay.", config.Tilde(dir)), false)
		if err != nil {
			return err
		}
		if !ok {
			return ui.ErrAborted
		}
	}
	if err := demo.Reset(); err != nil {
		return err
	}
	ui.Println(ui.OK("Demo data deleted  " + ui.Muted.Render(config.Tilde(dir))))
	return nil
}

// demoBlocked are commands that change real profiles; the demo has only
// its own.
var demoBlocked = map[string]bool{
	"paygate init":           true,
	"paygate profile rename": true,
	"paygate profile delete": true,
}

// startDemo switches to demo mode for --demo.
func startDemo(cmd *cobra.Command) error {
	if demoBlocked[cmd.CommandPath()] {
		return fmt.Errorf("`%s` is not available in demo mode: the demo has only its own profile %q. Run it without --demo", cmd.CommandPath(), demo.ProfileName)
	}
	_, err := demo.Start()
	return err
}
