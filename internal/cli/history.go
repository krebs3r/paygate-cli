package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/history"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type historyOptions struct {
	all   bool
	limit int
}

func newHistoryCmd(app *App) *cobra.Command {
	o := &historyOptions{}
	cmd := &cobra.Command{
		Use:     "history",
		Aliases: []string{"payments", "list"},
		Short:   "List payments created or looked up with paygate (local history)",
		Long: `List the payments created or looked up with paygate.

Paygate has no endpoint to list payments, so paygate keeps a local history
(IDs, amounts and states, no card data) in its configuration folder. Set
PAYGATE_NO_HISTORY=1 to switch it off.`,
		Example: `  paygate history
  paygate history --all --limit 50
  paygate history -o json | jq '.[0].payId'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHistory(app, o)
		},
	}
	cmd.Flags().BoolVar(&o.all, "all", false, "show all profiles, not only the active one")
	cmd.Flags().IntVarP(&o.limit, "limit", "n", 20, "number of payments to show (0 = all)")
	return cmd
}

func runHistory(app *App, o *historyOptions) error {
	records, err := history.Load()
	if err != nil {
		return err
	}
	profile := ""
	if !o.all {
		name, _, err := app.Profile()
		if err != nil {
			return err
		}
		profile = name
	}
	var rows []history.Record
	for _, r := range records {
		if profile == "" || r.Profile == profile {
			rows = append(rows, r)
		}
		if o.limit > 0 && len(rows) == o.limit {
			break
		}
	}

	if app.JSON() {
		if rows == nil {
			rows = []history.Record{}
		}
		b, _ := json.MarshalIndent(rows, "", "  ")
		ui.Println(string(b))
		return nil
	}
	if len(rows) == 0 {
		ui.Println(ui.Info("No payments yet. Create one with " + ui.Link.Render("paygate pay") + " or look one up with " + ui.Link.Render("paygate get <payId>") + "."))
		return nil
	}

	headers := []string{"Time", "Status", "Amount", "Method", "Trans ID", "Pay ID"}
	if o.all {
		headers = append([]string{"Profile"}, headers...)
	}
	var table [][]string
	for _, r := range rows {
		status := r.Status
		switch paygate.OutcomeOf(&paygate.Payment{Status: r.Status, ResponseCode: paygate.Code(r.Code)}) {
		case paygate.OutcomeSuccess:
			status = "✓ " + status
		case paygate.OutcomeFailed:
			status = "✗ " + status
		default:
			status = "… " + status
		}
		amount := money.Format(r.Amount, r.Currency)
		if r.Simulation != "" {
			amount += " ◆"
		}
		row := []string{r.Time.Local().Format("02.01. 15:04"), status, amount, r.Method, r.TransID, short(r.PayID)}
		if o.all {
			row = append([]string{r.Profile}, row...)
		}
		table = append(table, row)
	}
	ui.Println()
	ui.Println(ui.Table(headers, table))
	note := fmt.Sprintf("%d payments", len(rows))
	if strings.Contains(fmt.Sprint(table), "◆") {
		note += " · ◆ simulated"
	}
	ui.Println(ui.Muted.Render(" " + note))
	ui.Println(ui.Next("paygate get <payId>", "paygate refund <payId>", "paygate"))
	ui.Println()
	return nil
}
