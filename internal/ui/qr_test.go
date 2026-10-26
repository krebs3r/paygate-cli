package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"rsc.io/qr"
)

var sgrCell = regexp.MustCompile(`\x1b\[([0-9;]+)m(?:\x1b\[([0-9;]+)m)?([▄ ])`)

// decode turns the coloured terminal QR back into a module matrix.
func decode(t *testing.T, s string) [][]bool {
	t.Helper()
	var rows [][]bool
	for _, line := range strings.Split(s, "\n") {
		var top, bottom []bool
		for _, m := range sgrCell.FindAllStringSubmatch(line, -1) {
			codes := strings.Split(m[1]+";"+m[2], ";")
			var fgDark, bgDark bool
			for _, c := range codes {
				switch c {
				case "30":
					fgDark = true
				case "40":
					bgDark = true
				}
			}
			if m[3] == " " { // uniform cell: only the background counts
				fgDark = bgDark
			}
			// "▄": the background is the top module, the glyph the bottom one
			top, bottom = append(top, bgDark), append(bottom, fgDark)
		}
		rows = append(rows, top, bottom)
	}
	return rows
}

func TestQRMatchesEncoder(t *testing.T) {
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(termenv.Ascii)

	text := "https://test.paygate.nexigroup.com?token=ce0aef10a5b84c1e9d3f0b7e2a6c4d81"
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	rows := decode(t, QR(text))
	width := code.Size + 2*qrQuietSide
	height := code.Size + qrQuietTop + qrQuietBottom
	if len(rows[0]) != width || len(rows) != height {
		t.Fatalf("size = %d x %d modules, want %d x %d", len(rows[0]), len(rows), width, height)
	}
	for _, x := range rows[0] {
		if x || rows[1][len(rows[1])-1] {
			t.Fatal("the first character row must be completely light")
		}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			want := false
			if mx, my := x-qrQuietSide, y-qrQuietTop; mx >= 0 && my >= 0 && mx < code.Size && my < code.Size {
				want = code.Black(mx, my)
			}
			if rows[y][x] != want {
				t.Fatalf("module (%d,%d) = %v, want %v", x, y, rows[y][x], want)
			}
		}
	}
}

func TestQRPlainFallback(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	out := QR("hello")
	if strings.Contains(out, "\x1b[") || !strings.ContainsAny(out, "█▀▄") {
		t.Errorf("plain QR should use block characters without colours:\n%s", out)
	}
}

// A terminal too narrow for the QR code and its explanation side by side
// shows the explanation below the code instead of dropping it.
func TestCheckoutBlockKeepsTextWhenNarrow(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	url := "https://test.computop-paygate.com?token=7186c6fbec6d43d3b1b731dff5497ffe"
	lines := []string{"Link ready to share", "Card, PayPal · valid until 27 Oct 12:34", "Send it to your customer or scan it to pay yourself."}
	narrow := lipgloss.Width(QR(url)) + 10
	for w, beside := range map[int]bool{narrow: false, 200: true} {
		out := checkoutBlock("Payment link", url, lines, true, w)
		for _, want := range append(lines, "Payment link  "+url) {
			if !strings.Contains(out, want) {
				t.Errorf("width %d misses %q:\n%s", w, want, out)
			}
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, lines[0]) && strings.ContainsAny(l, "█▀▄") != beside {
				t.Errorf("width %d: text beside the code = %v, want %v", w, !beside, beside)
			}
		}
	}
}
