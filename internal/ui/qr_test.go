package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"rsc.io/qr"
)

var sgrCell = regexp.MustCompile(`\x1b\[([0-9;]+)m(?:\x1b\[([0-9;]+)m)?([▀ ])`)

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
			top, bottom = append(top, fgDark), append(bottom, bgDark)
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
