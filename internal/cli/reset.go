package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/config"
	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/reqlog"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newResetCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Delete all profiles, API keys, history and settings (factory reset)",
		Long: `Delete everything paygate stored on this computer, as right after the
installation: all profiles with their API and HMAC keys in the keychain, the settings,
the local payment history and the request log.

Payments at Paygate are not affected. The paygate program itself stays
installed.`,
		Example: `  paygate reset
  paygate reset --yes   # in scripts, without asking`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runReset(app) },
	}
}

func runReset(app *App) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if !app.Yes {
		if !ui.Interactive() {
			return errors.New("refusing to delete all data without --yes")
		}
		ui.Println(resetSummary(dir))
		ui.Println()
		ok, err := ui.Confirm("Delete all paygate data?", "This cannot be undone.", false)
		if err != nil {
			return err
		}
		if !ok {
			return ui.ErrAborted
		}
	}
	if _, err := config.Reset(); err != nil {
		return err
	}
	ui.Println(ui.OK("All data deleted  " + ui.Muted.Render(config.Tilde(dir))))
	ui.Println(ui.Next("paygate init"))
	return nil
}

// resetSummary lists what a reset deletes.
func resetSummary(dir string) string {
	profiles := "no profiles"
	if cfg, err := config.Load(); err == nil && len(cfg.Profiles) > 0 {
		keys := "their API keys"
		if len(cfg.Profiles) == 1 {
			keys = "its API key"
		}
		profiles = fmt.Sprintf("%s (%s) and %s", count(len(cfg.Profiles), "profile"), strings.Join(cfg.Names(), ", "), keys)
	}
	recs, _ := history.Load()
	logs, _ := reqlog.Load()
	return strings.Join([]string{
		ui.Warn("This deletes everything paygate stored in " + config.Tilde(dir) + ":"),
		"   " + profiles,
		"   " + count(len(recs), "payment") + " in the local history",
		"   " + count(len(logs), "request") + " in the request log",
		"   the settings",
		ui.Info("Payments at Paygate are not affected."),
	}, "\n")
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
