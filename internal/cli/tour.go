package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/tour"
	"github.com/krebs3r/paygate-cli/internal/ui"
)

func newTourCmd(app *App) *cobra.Command {
	var run bool
	var exportPath string
	var timeout time.Duration
	var ids []string
	for _, t := range tour.All() {
		ids = append(ids, t.ID)
	}
	cmd := &cobra.Command{
		Use:   "tour [" + strings.Join(ids, "|") + "]",
		Short: "Explain a Paygate integration step by step; --run sends the requests",
		Long: `Walk through a Paygate integration step by step: a sequence diagram, what
happens in each step and why, and with --run the real requests and answers.

Without an argument, paygate tour lists the tours. With a tour and no --run it
only explains; nothing is sent. With --run the requests go to the active
profile, best the demo (--demo): no credentials needed and nothing leaves
this computer. Tours do not run on production profiles.

--export writes a log of the run to a file, as HTML (.html) or Markdown
(.md), to send to someone who does not have paygate: every step with its
explanation, request, curl command and answer. Credentials appear only as
$PAYGATE_MERCHANT_ID and $PAYGATE_API_KEY; the demo Paygate is replaced by
the test host of the platform, other addresses on this computer by
shop.example.

Tours speak the language of the app (paygate profile, Settings).`,
		Example: `  paygate tour
  paygate tour hosted
  paygate --demo tour hosted --run
  paygate --demo tour hosted --run --export hosted.html
  paygate --demo tour operations --run --export operations.md`,
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: ids,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg, err := app.Config(); err == nil {
				i18n.Set(i18n.Detect(cfg.UILanguage))
			}
			if len(args) == 0 {
				listTours()
				return nil
			}
			t := tour.Find(args[0])
			if t == nil {
				return fmt.Errorf("unknown tour %q (use %s)", args[0], strings.Join(ids, ", "))
			}
			var format tour.Format
			if exportPath != "" {
				if !run {
					return errors.New("--export writes the log of a run: add --run")
				}
				f, err := tour.FormatOf(exportPath)
				if err != nil {
					return err
				}
				format = f
			}
			if !run {
				brand := ""
				if _, p, err := app.Profile(); err == nil {
					brand = p.Brand
				}
				explainTour(t, brand)
				return nil
			}
			return runTour(cmd.Context(), app, t, timeout, exportPath, format)
		},
	}
	cmd.Flags().BoolVar(&run, "run", false, "send the requests of the tour to the active profile")
	cmd.Flags().StringVar(&exportPath, "export", "", "write a log of the run to `file` (.html or .md), without credentials")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "how long a step waits for the customer or a webhook")
	return cmd
}

func listTours() {
	ui.Println()
	ui.Println(" " + ui.Bold.Render(tt("Integration tours", "Integrations-Touren")))
	ui.Println()
	w := 0
	for _, t := range tour.All() {
		w = max(w, lipgloss.Width(t.Title.String()))
	}
	for _, t := range tour.All() {
		title := t.Title.String()
		ui.Println("  " + ui.Accent.Render(fmt.Sprintf("%-11s", t.ID)) + ui.Bold.Render(title) + strings.Repeat(" ", w-lipgloss.Width(title)) +
			ui.Muted.Render(fmt.Sprintf("  %d %s · %s", len(t.Steps), tt("steps", "Schritte"), t.Shows)))
	}
	ui.Println()
	ui.Println(ui.Next("paygate tour hosted", "paygate --demo tour hosted --run"))
}

// printTourHead prints the title of a tour.
func printTourHead(t *tour.Tour) {
	ui.Println(" " + ui.Bold.Render(tt("Integration tour", "Integrations-Tour")+" · "+t.Title.String()))
	ui.Println()
}

// tt picks the English or German text.
func tt(en, de string) string { return tour.T{EN: en, DE: de}.String() }

func explainTour(t *tour.Tour, brand string) {
	ui.Println()
	printTourHead(t)
	ui.Println(ui.IndentLines(tour.Diagram(t, len(t.Steps), 72), " "))
	for i, st := range t.Steps {
		printStepHead(t, i, st, brand)
	}
	ui.Println()
	ui.Println(ui.Next("paygate --demo tour " + t.ID + " --run"))
}

// printStepHead prints a step with its explanation and the link to the
// documentation of the profile's platform (brand).
func printStepHead(t *tour.Tour, i int, st *tour.Step, brand string) {
	ui.Println()
	ui.Println(" " + ui.Accent.Render(fmt.Sprintf("%d/%d", i+1, len(t.Steps))) + "  " + ui.Bold.Render(st.Title.String()) + "  " + ui.Muted.Render(st.Kind.Label().String()))
	for _, l := range strings.Split(lipgloss.NewStyle().Width(76).Render(st.Explain.String()), "\n") {
		ui.Println("      " + ui.Label.Render(strings.TrimRight(l, " ")))
	}
	if u := st.Doc.URL(brand); u != "" {
		ui.Println("      " + ui.Label.Render(st.Doc.Label(brand)) + "  " + ui.Link.Render(u))
	}
}

// runTour sends the tour's requests with the active profile and prints
// every step with its request and answer. With exportPath it then writes
// the log of the run, also when a step failed.
func runTour(ctx context.Context, app *App, t *tour.Tour, timeout time.Duration, exportPath string, format tour.Format) (err error) {
	name, prof, client, err := app.Client()
	if err != nil {
		return err
	}
	r, err := tour.Start(t, &core.Session{Name: name, Profile: prof, Client: client}, app.Build.Version)
	switch {
	case errors.Is(err, tour.ErrProduction), errors.Is(err, tour.ErrNoWebhooks):
		return err
	case err != nil:
		return err
	}
	defer r.Close()
	if exportPath != "" {
		defer func() {
			if !r.Ran() {
				return
			}
			if werr := os.WriteFile(exportPath, tour.Export(r, format, time.Now()), 0o644); werr != nil {
				err = errors.Join(err, werr)
				return
			}
			ui.Println(ui.OK(tt("Log saved", "Protokoll gespeichert") + "  " + ui.Bold.Render(exportPath) + ui.Muted.Render(" "+tt("(without credentials)", "(ohne Zugangsdaten)"))))
		}()
	}
	app.PrintHeader(name, prof)
	printTourHead(t)
	ui.Println(ui.IndentLines(tour.Diagram(t, len(t.Steps), 72), " "))
	if ctx == nil {
		ctx = context.Background()
	}
	for i, st := range t.Steps {
		r.Go(i)
		printStepHead(t, i, st, prof.Brand)
		if p, err := r.Plan(); err == nil && p != nil {
			ui.Println()
			printRequest(p.Prepared.Method, p.Prepared.URL, p.Prepared.Body, p.Notes)
		}
		o := execUntilDecided(ctx, r, timeout)
		ui.Println()
		printOutcome(r, o)
		if o.Err != nil {
			return o.Err
		}
	}
	ui.Println()
	ui.Println(ui.OK(tt("Tour finished", "Tour abgeschlossen")))
	return nil
}

// execUntilDecided runs a step again while it waits, printing each new
// state once.
func execUntilDecided(ctx context.Context, r *tour.Run, timeout time.Duration) *tour.Outcome {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		o := r.Exec(ctx)
		if !o.Pending {
			return o
		}
		if o.Status != last {
			ui.Println("      " + ui.Muted.Render("… "+o.Status))
			last = o.Status
		}
		if time.Now().After(deadline) {
			return &tour.Outcome{Err: fmt.Errorf("gave up waiting after %s", timeout)}
		}
		select {
		case <-ctx.Done():
			return &tour.Outcome{Err: ctx.Err()}
		case <-time.After(time.Second):
		}
	}
}

func printRequest(method, url string, body []byte, notes map[string]tour.T) {
	ui.Println("      " + ui.Bold.Render(method) + " " + ui.Text.Render(url))
	for _, l := range tour.Annotate(body, notes) {
		ui.Println("      " + l)
	}
}

func printOutcome(r *tour.Run, o *tour.Outcome) {
	switch {
	case o.Err != nil:
		ui.Println("      " + ui.Danger.Render("✗ ") + o.Err.Error())
		return
	case o.Skipped != "":
		ui.Println("      " + ui.Warning.Render("↷ ") + ui.Label.Render(o.Skipped))
		return
	}
	if x := o.Exchange; x != nil && x.Status != 0 {
		status := fmt.Sprintf("%d %s", x.Status, http.StatusText(x.Status))
		style := ui.Success
		if x.Status >= 400 {
			style = ui.Danger
		}
		ui.Println("      " + style.Render(status) + ui.Muted.Render(" · "+x.Duration.Round(time.Millisecond).String()))
		for _, l := range tour.Annotate(x.Response, nil) {
			ui.Println("      " + l)
		}
	}
	if w := o.Webhook; w != nil {
		for _, name := range []string{"X-Paygate-Signature-Version", "X-Paygate-Timestamp", "X-Paygate-Signature"} {
			if v := w.Header.Get(name); v != "" {
				ui.Println("      " + ui.Label.Render(name+": ") + ui.Text.Render(v))
			}
		}
		for _, l := range tour.Annotate(w.Body, nil) {
			ui.Println("      " + l)
		}
	}
	for _, l := range o.Lines {
		ui.Println("      " + l)
	}
	if o.Status != "" {
		ui.Println("      " + o.Status)
	}
}
