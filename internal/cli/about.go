package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newAboutCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "about",
		Short: "Version, license and credits",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := app.Build
			rows := []ui.Row{
				{Label: "Version", Value: b.Version},
				{Label: "Commit", Value: ui.Muted.Render(b.Commit)},
				{Label: "Built", Value: ui.Muted.Render(b.Date)},
				{Label: "Author", Value: "Martin Krebs"},
				{Label: "License", Value: "Apache-2.0 · keep the NOTICE file when you redistribute"},
				{Label: "Inspired by", Value: "mollie-cli by Florian Bender and Hannes Reinberger"},
				{Label: "", Value: ui.Link.Render("https://github.com/fjbender/mollie-cli")},
			}
			ui.Println()
			if ui.IsTTY(os.Stdout) && ui.Width() >= ui.BigLogoWidth+4 {
				ui.Println(ui.IndentLines(ui.BigLogo(0), "  "))
				ui.Println()
			}
			ui.Println(ui.Card{
				Badge: ui.Accent.Render("◆"),
				Title: "paygate · Paygate CLI",
				Rows:  rows,
				Footer: ui.Warning.Render("! ") + ui.Muted.Render("Unofficial tool, not made or supported by Nexi or Computop.") + "\n" +
					ui.Warning.Render("! ") + ui.Muted.Render("Sends real requests; on production real money moves. No warranty.") + "\n" +
					ui.Muted.Render("Sister project: ") + ui.Link.Render("https://paygate.paytest.dev"),
			}.String())
			ui.Println()
			return nil
		},
	}
}
