package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/cards"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newTestcardsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:     "testcards",
		Aliases: []string{"cards"},
		Short:   "List the Paygate test cards and their aliases",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if app.JSON() {
				type card struct {
					Alias        string `json:"alias"`
					Brand        string `json:"brand"`
					Number       string `json:"number"`
					Scenario     string `json:"scenario"`
					Expect       string `json:"expect"`
					CardCurrency string `json:"cardCurrency,omitempty"`
				}
				out := make([]card, 0, len(cards.Cards)+len(cards.DCCCards))
				for _, c := range cards.Cards {
					out = append(out, card{c.Alias, c.Brand, c.Number, c.Scenario, c.Expect, ""})
				}
				for _, d := range cards.DCCCards {
					c := d.Card()
					out = append(out, card{c.Alias, c.Brand, c.Number, c.Scenario, c.Expect, d.Currency})
				}
				b, _ := json.MarshalIndent(out, "", "  ")
				ui.Println(string(b))
				return nil
			}
			rows := make([][]string, len(cards.Cards))
			for i, c := range cards.Cards {
				rows[i] = []string{c.Alias, brandName(c.Brand), formatPAN(c.Number), c.Scenario, c.Expect}
			}
			ui.Println()
			ui.Println(ui.Table([]string{"Alias", "Brand", "Number", "Scenario", "Expected"}, rows))
			ui.Println()
			ui.Println(ui.Info("Any future expiry date and any CVC work. 3DS cards need 3-D Secure enabled on your test MID."))
			otp := ""
			for i, o := range cards.ChallengeOTPs {
				if i > 0 {
					otp += ui.Muted.Render(" · ")
				}
				otp += ui.Bold.Render(o.Code) + " " + ui.Muted.Render(o.Result)
			}
			ui.Println(ui.Info("Challenge OTPs: " + otp))
			ui.Println()
			dcc := make([][]string, len(cards.DCCCards))
			for i, c := range cards.DCCCards {
				dcc[i] = []string{c.Alias, brandName(c.Brand), formatPAN(c.Number), c.Currency, c.Expect()}
			}
			ui.Println(ui.Table([]string{"eDCC alias", "Brand", "Number", "Card currency", "Expected"}, dcc))
			ui.Println()
			ui.Println(ui.Info("eDCC cards from the Nexi test guide: test system only, with eDCC enabled for the merchant."))
			ui.Println()
			ui.Println(ui.Next("paygate pay --card visa-ok", "paygate pay --card visa-decline", "paygate dcc quote dcc-usd"))
			ui.Println()
			return nil
		},
	}
}
