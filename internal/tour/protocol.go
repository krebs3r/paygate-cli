package tour

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/krebs3r/paygate-cli/internal/core"
	"github.com/krebs3r/paygate-cli/internal/export"
	"github.com/krebs3r/paygate-cli/internal/i18n"
	"github.com/krebs3r/paygate-cli/internal/paygate"
	"github.com/krebs3r/paygate-cli/internal/webhooks"
)

// Format is the file format of an exported protocol.
type Format string

const (
	// HTML is one file with its styles inside, to attach to an e-mail.
	HTML Format = "html"
	// Markdown suits wikis and tickets.
	Markdown Format = "md"
)

// FormatOf picks the format by the file name: .html or .htm, .md or
// .markdown.
func FormatOf(name string) (Format, error) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return HTML, nil
	case ".md", ".markdown":
		return Markdown, nil
	}
	return "", fmt.Errorf("%q: the protocol is written as .html or .md", name)
}

// FileName suggests a name for the protocol of a run, e.g.
// paygate-tour-hosted-20261118-1405.html.
func FileName(r *Run, f Format, at time.Time) string {
	return fmt.Sprintf("paygate-tour-%s-%s.%s", r.Tour.ID, at.Format("20060102-1504"), f)
}

// Label names the kind of a step.
func (k Kind) Label() T {
	switch k {
	case Request:
		return T{"API request", "API-Request"}
	case Browser:
		return T{"browser", "Browser"}
	case Wait:
		return T{"waits", "wartet"}
	case Local:
		return T{"local", "lokal"}
	}
	return T{"not run", "nicht ausgeführt"}
}

// Ran reports whether at least one step has run, so there is something to
// put in a protocol.
func (r *Run) Ran() bool {
	for i := range r.Tour.Steps {
		if r.Done(i) {
			return true
		}
	}
	return false
}

// Export writes the protocol of a run: every step with its explanation,
// the link to the documentation, the request, the request as a curl command
// and the answer, as far as the run got. Credentials appear only as the
// variables $PAYGATE_MERCHANT_ID, $PAYGATE_API_KEY and $PAYGATE_HMAC_KEY.
// Addresses on this computer are replaced: the demo Paygate by the test
// host of the profile's platform, all others by shop.example.
func Export(r *Run, f Format, at time.Time) []byte {
	p := r.protocol(at)
	if f == Markdown {
		return p.markdown()
	}
	return p.html()
}

// protocol is a run as plain text, cleaned of credentials and local
// addresses.
type protocol struct {
	lang      string
	title     string
	eyebrow   string
	meta      []string
	env       string // DEMO or TEST
	notes     []string
	diagram   string
	flowTitle string
	steps     []protoStep
	footer    string
}

type protoStep struct {
	n                    int
	title, kind, explain string
	docLabel, docURL     string
	blocks               []block
	status               string
	statusTone           string // ok, bad, warn, muted
}

// block is a piece of code: a request, a curl command, an answer. html is
// the text with syntax colours, if it has them.
type block struct {
	label, lang, text, html string
	wrap                    bool // prose that may wrap
}

func (r *Run) protocol(at time.Time) *protocol {
	prof := r.Session.Profile
	c := newCleaner(r)
	p := &protocol{
		lang:      string(i18n.Current()),
		title:     r.Tour.Title.String(),
		eyebrow:   T{"Integration log", "Integrationsprotokoll"}.String(),
		env:       strings.ToUpper(prof.Env),
		diagram:   ansi.Strip(Diagram(r.Tour, len(r.Tour.Steps), 72)),
		flowTitle: T{"Flow", "Ablauf"}.String(),
		footer:    T{"Made with paygate ", "Erstellt mit paygate "}.String() + r.version + " · https://github.com/krebs3r/paygate-cli",
	}
	if p.lang == "" {
		p.lang = "en"
	}
	when := at.Format("02 Jan 2006, 15:04")
	if i18n.Current() == i18n.DE {
		when = at.Format("02.01.2006, 15:04")
	}
	envLabel := T{"test system", "Testsystem"}
	if prof.IsDemo() {
		envLabel = T{"demo mode", "Demo-Modus"}
	}
	p.meta = []string{when, "paygate " + r.version, envLabel.String(), paygate.BrandLabel(paygate.Brand(prof.Brand))}

	done, skipped, failed, open := 0, 0, 0, 0
	brand := prof.Brand
	for i, st := range r.Tour.Steps {
		o := r.Outcome(i)
		ps := protoStep{n: i + 1, title: st.Title.String(), kind: st.Kind.Label().String(), explain: st.Explain.String(),
			docLabel: st.Doc.Label(brand), docURL: st.Doc.URL(brand)}
		switch {
		case o == nil:
			open++
			ps.status, ps.statusTone = "○ "+T{"Not run: the tour ended before this step.", "Nicht ausgeführt: Die Tour endete vor diesem Schritt."}.String(), "muted"
		case o.Err != nil:
			failed++
			ps.blocks = r.blocks(o, c)
			ps.status, ps.statusTone = "✗ "+c.text(o.Err.Error()), "bad"
		case o.Skipped != "":
			skipped++
			ps.status, ps.statusTone = "↷ "+T{"Skipped: ", "Übersprungen: "}.String()+c.text(o.Skipped), "warn"
		default:
			done++
			ps.blocks = r.blocks(o, c)
			ps.status = c.text(ansi.Strip(o.Status))
			if ps.status == "" {
				ps.status = T{"Done", "Erledigt"}.String()
			}
			ps.statusTone = toneOf(ps.status)
			if ps.statusTone == "" {
				ps.status, ps.statusTone = "✓ "+ps.status, "ok"
			}
		}
		p.steps = append(p.steps, ps)
	}
	n := len(r.Tour.Steps)
	summary := fmt.Sprintf(T{"%d steps, all run", "%d Schritte, alle durchlaufen"}.String(), n)
	if done < n {
		parts := []string{fmt.Sprintf(T{"%d steps", "%d Schritte"}.String(), n)}
		for _, part := range []struct {
			n    int
			text T
		}{
			{skipped, T{"%d skipped", "%d übersprungen"}},
			{failed, T{"%d failed", "%d fehlgeschlagen"}},
			{open, T{"%d not run", "%d nicht ausgeführt"}},
		} {
			if part.n > 0 {
				parts = append(parts, fmt.Sprintf(part.text.String(), part.n))
			}
		}
		summary = strings.Join(parts, ", ")
	}
	p.meta = append(p.meta, summary)

	switch {
	case prof.IsDemo():
		p.notes = append(p.notes, T{"Answers in demo mode are simulated. What counts is the Paygate API reference.",
			"Antworten im Demo-Modus sind nachgebildet. Verbindlich ist die API-Referenz von Paygate."}.String())
	case core.LocalOrigin(prof):
		p.notes = append(p.notes, T{"The answers come from a simulated Paygate. What counts is the Paygate API reference.",
			"Die Antworten stammen von einem nachgebildeten Paygate. Verbindlich ist die API-Referenz von Paygate."}.String())
	}
	p.notes = append(p.notes, T{"No credentials in this log: the curl commands read merchant ID and API key from $PAYGATE_MERCHANT_ID and $PAYGATE_API_KEY.",
		"Zugangsdaten stehen nicht in diesem Protokoll: Die curl-Befehle lesen Merchant ID und API-Key aus $PAYGATE_MERCHANT_ID und $PAYGATE_API_KEY."}.String())
	if c.usedHost || c.usedLocal {
		var what []string
		if c.usedHost {
			what = append(what, fmt.Sprintf(T{"the simulated Paygate by the test host %s", "der nachgebildete Paygate durch den Test-Host %s"}.String(), strings.TrimPrefix(c.host, "https://")))
		}
		if c.usedLocal {
			what = append(what, T{"the addresses of your server by shop.example", "die Adressen deines Servers durch shop.example"}.String())
		}
		p.notes = append(p.notes, T{"Addresses on the computer the tour ran on are replaced: ", "Adressen auf dem Rechner, auf dem die Tour lief, sind ersetzt: "}.String()+
			strings.Join(what, T{" and ", " und "}.String())+".")
	}
	return p
}

// toneOf reads the tone of a status line from its first sign; "" if it has
// none.
func toneOf(s string) string {
	switch {
	case strings.HasPrefix(s, "✓"):
		return "ok"
	case strings.HasPrefix(s, "✗"):
		return "bad"
	case strings.HasPrefix(s, "!"), strings.HasPrefix(s, "↩"), strings.HasPrefix(s, "↷"), strings.HasPrefix(s, "…"):
		return "warn"
	case strings.HasPrefix(s, "○"), strings.HasPrefix(s, "·"), strings.HasPrefix(s, "→"):
		return "muted"
	}
	return ""
}

// blocks are the request, curl command, answer, webhook and further lines
// of an outcome.
func (r *Run) blocks(o *Outcome, c *cleaner) []block {
	var out []block
	if x := o.Exchange; x != nil {
		headers := make([][2]string, len(x.Headers))
		for i, h := range x.Headers {
			headers[i] = [2]string{h[0], c.text(h[1])}
		}
		req := httpBlock(x.Method+" "+c.text(x.URL), headers, c.bytes(x.Body), x.Notes)
		req.label = "Request"
		out = append(out, req)
		if x.shared != "" {
			curl := c.text(x.shared)
			out = append(out, block{label: T{"Request as curl", "Request als curl"}.String(), lang: "bash", text: curl, html: htmlCurl(curl)})
		}
		if x.Status != 0 {
			label := T{"Answer", "Antwort"}.String() + " · " + fmt.Sprintf("%d %s", x.Status, http.StatusText(x.Status))
			if c.origin != "" {
				label += " · " + T{"simulated", "nachgebildet"}.String()
			}
			body := c.bytes(x.Response)
			out = append(out, block{label: label, lang: "json", text: plainJSON(body, nil), html: htmlJSONOf(body, nil)})
		}
	}
	if w := o.Webhook; w != nil {
		target := r.WebhookURL()
		if target == "" {
			target = "urls.webhook"
		}
		var headers [][2]string
		for _, name := range []string{"Content-Type", webhooks.HeaderVersion, webhooks.HeaderTimestamp, webhooks.HeaderSignature} {
			if v := w.Header.Get(name); v != "" {
				headers = append(headers, [2]string{name, v})
			}
		}
		hook := httpBlock("POST "+c.text(target), headers, c.bytes(w.Body), nil)
		hook.label = "Webhook"
		out = append(out, hook)
	}
	if len(o.Lines) > 0 {
		lines := make([]string, len(o.Lines))
		for i, l := range o.Lines {
			lines[i] = strings.TrimRight(c.text(ansi.Strip(l)), " ")
		}
		out = append(out, block{lang: "text", text: strings.Trim(strings.Join(lines, "\n"), "\n"), wrap: true})
	}
	return out
}

// httpBlock is a request as HTTP: the request line, the headers and the
// JSON body with the field notes.
func httpBlock(line string, headers [][2]string, body []byte, notes map[string]T) block {
	method, rest, _ := strings.Cut(line, " ")
	text := line
	marked := `<span class="h-m">` + html.EscapeString(method) + `</span> ` + html.EscapeString(rest)
	for _, h := range headers {
		text += "\n" + h[0] + ": " + h[1]
		marked += "\n" + `<span class="h-k">` + html.EscapeString(h[0]+":") + `</span> ` + html.EscapeString(h[1])
	}
	if plain := plainJSON(body, notes); plain != "" {
		text += "\n\n" + plain
		marked += "\n\n" + htmlJSONOf(body, notes)
	}
	return block{lang: "http", text: text, html: marked}
}

// htmlJSONOf is a body pretty-printed as HTML with syntax colours and the
// field notes.
func htmlJSONOf(body []byte, notes map[string]T) string {
	lines := annotate(body, notes, htmlJSON)
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

// paygateVarRe finds the variables a reader sets before running a command.
var paygateVarRe = regexp.MustCompile(`\$PAYGATE_[A-Z_]+`)

// htmlCurl is a curl command as HTML. It stays plain, since its body is the
// request shown above, except for the $PAYGATE_ variables the reader has to
// set before running it.
func htmlCurl(cmd string) string {
	tip := html.EscapeString(T{"Set this variable before you run the command", "Setze diese Variable, bevor du den Befehl ausführst"}.String())
	return paygateVarRe.ReplaceAllStringFunc(html.EscapeString(cmd), func(v string) string {
		return `<span class="v" title="` + tip + `">` + v + `</span>`
	})
}

// plainJSON is a body pretty-printed, with the field notes behind their
// lines, without colours.
func plainJSON(body []byte, notes map[string]T) string {
	lines := Annotate(body, notes)
	for i, l := range lines {
		lines[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(lines, "\n")
}

// cleaner takes credentials and local addresses out of what a protocol
// shows.
type cleaner struct {
	origin, host string // the simulated Paygate and the test host for it
	secrets      *strings.Replacer
	merchant     string

	usedHost, usedLocal bool
}

// Replacements for credentials and addresses on this computer.
const (
	shopExample = "https://shop.example"
	hidden      = "•••"
)

var loopbackRe = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost|\[::1\])(?::\d+)?`)

func newCleaner(r *Run) *cleaner {
	prof, client := r.Session.Profile, r.Session.Client
	c := &cleaner{merchant: client.MerchantID}
	if core.LocalOrigin(prof) {
		c.origin = client.Origin
		c.host, _ = paygate.Origin(paygate.Brand(prof.Brand), paygate.EnvTest)
		if c.host == "" {
			c.host, _ = paygate.Origin(paygate.BrandNexi, paygate.EnvTest)
		}
	}
	// Longest first, so a key that contains another is replaced whole.
	type pair struct{ secret, with string }
	var pairs []pair
	add := func(secret, with string) {
		if len([]rune(secret)) >= 4 {
			pairs = append(pairs, pair{secret, with})
		}
	}
	add(client.APIKey, "$"+export.APIKeyEnv)
	if k := r.HMACKey(); k != "" {
		add(k, "$PAYGATE_HMAC_KEY")
		add(short(k, 4), hidden) // the signature check names the key by its start
	}
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].secret) > len(pairs[j].secret) })
	var args []string
	for _, p := range pairs {
		args = append(args, p.secret, p.with)
	}
	c.secrets = strings.NewReplacer(args...)
	return c
}

func (c *cleaner) text(s string) string {
	if c.origin != "" && strings.Contains(s, c.origin) {
		s = strings.ReplaceAll(s, c.origin, c.host)
		c.usedHost = true
	}
	if loopbackRe.MatchString(s) {
		s = loopbackRe.ReplaceAllString(s, shopExample)
		c.usedLocal = true
	}
	s = c.secrets.Replace(s)
	return replaceID(s, c.merchant, "$"+export.MerchantIDEnv)
}

func (c *cleaner) bytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return []byte(c.text(string(b)))
}

// replaceID replaces id where it stands on its own, not as part of a longer
// name: "shop" in "shop-2" stays.
func replaceID(s, id, with string) string {
	if len(id) < 3 || !strings.Contains(s, id) {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, id)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := i + len(id)
		b.WriteString(s[:i])
		if (i == 0 || !idChar(s[i-1])) && (end == len(s) || !idChar(s[end])) {
			b.WriteString(with)
		} else {
			b.WriteString(id)
		}
		s = s[end:]
	}
}

func idChar(c byte) bool {
	return c == '_' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// markdown renders the protocol for wikis and tickets.
func (p *protocol) markdown() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n", p.title)
	meta := append([]string(nil), p.meta...)
	meta[2] = p.env + " (" + meta[2] + ")"
	fmt.Fprintf(&b, "*%s · %s*\n\n", p.eyebrow, strings.Join(meta, " · "))
	for _, n := range p.notes {
		fmt.Fprintf(&b, "> %s\n>\n", mdVars(n))
	}
	if len(p.notes) > 0 {
		b.Truncate(b.Len() - 2)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## %s\n\n", p.flowTitle)
	for _, s := range p.steps {
		fmt.Fprintf(&b, "%d. %s · %s\n", s.n, s.title, s.status)
	}
	fmt.Fprintf(&b, "\n```text\n%s\n```\n", strings.TrimRight(p.diagram, "\n"))
	for _, s := range p.steps {
		fmt.Fprintf(&b, "\n## %d · %s\n\n*%s*\n\n%s\n", s.n, s.title, s.kind, s.explain)
		if s.docURL != "" {
			fmt.Fprintf(&b, "\n[%s](%s)\n", s.docLabel, s.docURL)
		}
		for _, bl := range s.blocks {
			if bl.label != "" {
				fmt.Fprintf(&b, "\n**%s**\n", bl.label)
			}
			fmt.Fprintf(&b, "\n%s%s\n%s\n%s\n", fence(bl.text), bl.lang, bl.text, fence(bl.text))
		}
		fmt.Fprintf(&b, "\n%s\n", s.status)
	}
	fmt.Fprintf(&b, "\n---\n\n%s\n", p.footer)
	return b.Bytes()
}

// mdVars sets the variable names of a note in code style.
func mdVars(s string) string {
	return varRe.ReplaceAllString(s, "`$0`")
}

var varRe = regexp.MustCompile(`\$PAYGATE_[A-Z_]+`)

// fence is a code fence longer than any run of backticks in text.
func fence(text string) string {
	n := 3
	for run := 0; ; {
		i := strings.IndexByte(text, '`')
		if i < 0 {
			break
		}
		run = 1
		for i+run < len(text) && text[i+run] == '`' {
			run++
		}
		n = max(n, run+1)
		text = text[i+run:]
	}
	return strings.Repeat("`", n)
}

// html renders the protocol as one page with its styles inside.
func (p *protocol) html() []byte {
	e := html.EscapeString
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!doctype html>\n<html lang=\"%s\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n", e(p.lang))
	fmt.Fprintf(&b, "<title>%s · %s</title>\n<style>%s</style>\n</head>\n<body>\n<main>\n", e(p.title), e(p.eyebrow), protocolCSS)
	fmt.Fprintf(&b, "<header>\n<div class=\"eyebrow\">%s</div>\n<h1>%s</h1>\n<div class=\"meta\">", e(p.eyebrow), e(p.title))
	for i, m := range p.meta {
		if i == 2 {
			fmt.Fprintf(&b, "<span><span class=\"pill %s\">%s</span> %s</span>", strings.ToLower(e(p.env)), e(p.env), e(m))
			continue
		}
		fmt.Fprintf(&b, "<span>%s</span>", e(m))
	}
	b.WriteString("</div>\n</header>\n")
	for i, n := range p.notes {
		cls := "note"
		if i == 0 && strings.EqualFold(p.env, "demo") {
			cls += " demo"
		}
		fmt.Fprintf(&b, "<p class=\"%s\">%s</p>\n", cls, codeVars(e(n)))
	}
	fmt.Fprintf(&b, "<section>\n<h2>%s</h2>\n<ol class=\"flow\">\n", e(p.flowTitle))
	for _, s := range p.steps {
		fmt.Fprintf(&b, "<li><a href=\"#step-%d\">%s</a> <span class=\"%s\">%s</span></li>\n", s.n, e(s.title), s.statusTone, e(s.status))
	}
	fmt.Fprintf(&b, "</ol>\n<pre class=\"diagram\">%s</pre>\n</section>\n", e(strings.TrimRight(p.diagram, "\n")))
	for _, s := range p.steps {
		fmt.Fprintf(&b, "<section class=\"step\" id=\"step-%d\">\n<h2><span class=\"n\">%d</span> %s <span class=\"kind\">%s</span></h2>\n<p>%s</p>\n",
			s.n, s.n, e(s.title), e(s.kind), e(s.explain))
		if s.docURL != "" {
			fmt.Fprintf(&b, "<p class=\"doc\"><a href=\"%s\">%s</a></p>\n", e(s.docURL), e(s.docLabel))
		}
		for _, bl := range s.blocks {
			if bl.label != "" {
				fmt.Fprintf(&b, "<div class=\"lbl\">%s</div>\n", e(bl.label))
			}
			cls := ""
			if bl.wrap {
				cls = " class=\"wrap\""
			}
			text := e(bl.text)
			if bl.html != "" {
				text = bl.html
			}
			fmt.Fprintf(&b, "<pre%s>%s</pre>\n", cls, text)
		}
		fmt.Fprintf(&b, "<p class=\"status %s\">%s</p>\n</section>\n", s.statusTone, e(s.status))
	}
	fmt.Fprintf(&b, "<footer>%s</footer>\n</main>\n</body>\n</html>\n", linkify(e(p.footer)))
	return b.Bytes()
}

// codeVars sets the variable names of an escaped note in code style.
func codeVars(s string) string { return varRe.ReplaceAllString(s, "<code>$0</code>") }

// linkify turns the repository address of the footer into a link.
func linkify(s string) string {
	const repo = "https://github.com/krebs3r/paygate-cli"
	return strings.Replace(s, repo, `<a href="`+repo+`">github.com/krebs3r/paygate-cli</a>`, 1)
}

const protocolCSS = `
:root{--j-key:#1d4ed8;--j-str:#15803d;--j-num:#b45309;--j-lit:#7c3aed;--bg:#f4f6fa;--surface:#fff;--fg:#0f172a;--muted:#475569;--faint:#8391a7;--line:#d9e0eb;--accent:#2563eb;--code:#eef2f8;--demo:#7c3aed;--demo-soft:#efe7fe;--test:#b45309;--soft:#eef2f8;--ok:#15803d;--bad:#dc2626;--warn:#b45309;color-scheme:light}
@media (prefers-color-scheme:dark){:root{--j-key:#8cc4ff;--j-str:#4ade80;--j-num:#fbbf24;--j-lit:#c4b5fd;--bg:#0b0f16;--surface:#111722;--fg:#dce8f5;--muted:#9aaecb;--faint:#6b7d98;--line:#222c3d;--accent:#6fa0ff;--code:#0e141e;--demo:#b49cff;--demo-soft:#221a38;--test:#fbbf24;--soft:#151c28;--ok:#34d399;--bad:#f87171;--warn:#fbbf24;color-scheme:dark}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;padding:32px 16px 64px}
main{max-width:920px;margin:0 auto;background:var(--surface);border:1px solid var(--line);border-radius:12px;padding:30px clamp(16px,4vw,36px) 34px;display:grid;gap:22px}
h1,h2{margin:0;line-height:1.2;letter-spacing:-.01em}
h1{font-size:2rem}
h2{font-size:1.15rem;display:flex;flex-wrap:wrap;align-items:baseline;gap:4px 10px}
p{margin:0}
a{color:var(--accent)}
header{display:grid;gap:8px}
.eyebrow,.lbl{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.72rem;letter-spacing:.07em;text-transform:uppercase;color:var(--faint)}
.meta{display:flex;flex-wrap:wrap;gap:4px 18px;color:var(--muted);font-size:.9rem}
.pill{display:inline-block;font:700 .72rem ui-monospace,SFMono-Regular,Menlo,monospace;letter-spacing:.04em;padding:.1em .55em;border-radius:3px;color:#fff;background:var(--test)}
.pill.demo{background:#7c3aed}
.pill.test{color:#1a1204;background:#f59e0b}
.note{background:var(--soft);border-radius:8px;padding:10px 14px;font-size:.92rem}
.note.demo{background:var(--demo-soft)}
code{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;font-size:.86em}
section{display:grid;gap:10px}
section.step{border-top:1px solid var(--line);padding-top:20px}
.n{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--faint)}
.kind{font-size:.78rem;font-weight:500;color:var(--muted);border:1px solid var(--line);border-radius:4px;padding:0 .5em}
.doc{font-size:.92rem}
ol.flow{margin:0;padding-left:1.6em;display:grid;gap:3px}
ol.flow a{color:var(--fg);text-decoration:none;border-bottom:1px solid var(--line)}
ol.flow span{font-size:.9rem;margin-left:6px}
pre{margin:0;background:var(--code);border:1px solid var(--line);border-radius:8px;padding:12px 14px;font:12.5px/1.55 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;overflow-x:auto;color:var(--fg)}
pre.wrap{white-space:pre-wrap}
pre.diagram{background:none;border:0;padding:6px 0 0}
.j-k,.h-m{color:var(--j-key)}.j-s{color:var(--j-str)}.j-n{color:var(--j-num)}.j-l{color:var(--j-lit)}.j-p,.j-c{color:var(--faint)}.j-c b{color:var(--muted);font-weight:600}.h-m{font-weight:700}.h-k{color:var(--muted)}.v{color:var(--j-lit);font-weight:700}
.status{font-weight:600}
.ok{color:var(--ok)}.bad{color:var(--bad)}.warn{color:var(--warn)}.muted{color:var(--muted)}
footer{border-top:1px solid var(--line);padding-top:16px;color:var(--faint);font-size:.85rem}
@media print{body{background:#fff;padding:0}main{border:0}pre{white-space:pre-wrap}}
`
