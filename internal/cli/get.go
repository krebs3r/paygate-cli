package cli

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/money"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

type getOptions struct {
	transID string
}

func newGetCmd(app *App) *cobra.Command {
	o := &getOptions{}
	cmd := &cobra.Command{
		Use:     "get [payId]",
		Aliases: []string{"status"},
		Short:   "Show a payment by payId or --trans-id",
		Example: `  paygate get 6B29FC4093DD010662DE
  paygate get --trans-id TIDMBX8K2Q4F7A9C
  paygate get 6B29FC4093DD010662DE -o json | jq .status`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			payID := ""
			if len(args) == 1 {
				payID = args[0]
			}
			if (payID == "") == (o.transID == "") {
				return errors.New("pass either a payId or --trans-id")
			}
			name, prof, client, err := app.Client()
			if err != nil {
				return err
			}
			var prepared *paygate.Prepared
			if payID != "" {
				prepared, err = client.GetByPayIDRequest(payID)
			} else {
				prepared, err = client.GetByTransIDRequest(o.transID)
			}
			if err != nil {
				return err
			}
			if app.Curl {
				printCurl(app, prepared, client)
				return nil
			}
			app.PrintHeader(name, prof)
			var res *paygate.Result
			err = ui.Spin("Loading payment …", func() error {
				var e error
				res, e = client.Exec(context.Background(), prepared)
				return e
			})
			if err == nil {
				core.RecordStatus(name, res.Payment)
			}
			if err != nil {
				if app.JSON() {
					if e, ok := paygate.AsAPIError(err); ok && e.Response != nil {
						printJSON(e.Response.Body)
					}
				}
				return err
			}
			if app.JSON() {
				printJSON(res.Response.Body)
				return nil
			}
			ui.Println(paymentCard(res.Payment, res.Response, cardOpts{
				Title: "Payment details",
				Extra: amountRows(res.Payment),
			}))
			ui.Println()
			return nil
		},
	}
	cmd.Flags().StringVar(&o.transID, "trans-id", "", "look up by your transaction ID instead of the payId")
	return cmd
}

// amountRows shows captured / refunded amounts when Paygate reports them.
func amountRows(p *paygate.Payment) []ui.Row {
	if p.Amount == nil || p.Amount.Currency == "" {
		return nil
	}
	var rows []ui.Row
	if p.Amount.CapturedKnown {
		rows = append(rows, ui.Row{Label: "Captured", Value: money.Format(p.Amount.CapturedValue, p.Amount.Currency)})
	}
	if p.Amount.RefundedKnown {
		rows = append(rows, ui.Row{Label: "Refunded", Value: money.Format(p.Amount.RefundedValue, p.Amount.Currency)})
	}
	return rows
}
