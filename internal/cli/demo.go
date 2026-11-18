package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	cmd.AddCommand(newDemoScenarioCmd(app))
	return cmd
}

func newDemoScenarioCmd(app *App) *cobra.Command {
	var names []string
	for _, sc := range demo.ScenarioList {
		names = append(names, sc.Name)
	}
	return &cobra.Command{
		Use:   "scenario [name [value]]",
		Short: "Show or set how the demo Paygate behaves",
		Long: `Show or set the demo scenarios: how hosted payments end, 3-D Secure,
webhooks and their signature, eDCC, and a one-time error for the next API
call. Without arguments, paygate demo scenario lists them with their values;
the first value is the default. The app shows them under Settings in demo
mode.

The scenarios apply to the demo only and are kept in its folder; a demo
that runs in the app picks a change up with its next request.`,
		Example: `  paygate demo scenario
  paygate demo scenario checkout pay        # hosted payments pay by themselves
  paygate demo scenario signature wrong-key # webhooks signed with a wrong key
  paygate demo scenario fail-next 503       # the next API call answers 503`,
		Args:      cobra.MaximumNArgs(2),
		ValidArgs: names,
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := demo.LoadScenarios()
			if err != nil {
				return err
			}
			if len(args) == 2 {
				if err := sc.Set(args[0], args[1]); err != nil {
					return err
				}
				if err := demo.SaveScenarios(sc); err != nil {
					return err
				}
			}
			if app.JSON() {
				out := map[string]string{}
				for _, s := range demo.ScenarioList {
					out[s.Name] = sc.Get(s.Name)
				}
				b, err := json.Marshal(out)
				if err != nil {
					return err
				}
				printJSON(b)
				return nil
			}
			if len(args) == 2 {
				ui.Println(ui.OK(args[0] + " = " + sc.Get(args[0])))
				return nil
			}
			ui.Println()
			for _, s := range demo.ScenarioList {
				if len(args) == 1 && args[0] != s.Name {
					continue
				}
				v := sc.Get(s.Name)
				var opts []string
				for _, o := range s.Values {
					if o == v {
						opts = append(opts, ui.Bold.Render(o))
					} else {
						opts = append(opts, ui.Muted.Render(o))
					}
				}
				ui.Println("  " + ui.Accent.Render(fmt.Sprintf("%-10s", s.Name)) + strings.Join(opts, ui.Muted.Render(" · ")))
			}
			if len(args) == 1 && sc.Get(args[0]) == "" {
				return fmt.Errorf("unknown scenario %q (use %s)", args[0], strings.Join(names, ", "))
			}
			ui.Println()
			return nil
		},
	}
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
