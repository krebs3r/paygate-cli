package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"rsc.io/qr"
)

// Light border around the code, in modules. One module on the dark terminal
// background scans reliably; two on top make the first character row a clean
// white line (codes have an odd size, so 2 + size + 1 fills whole rows).
const (
	qrQuietSide   = 1
	qrQuietTop    = 2
	qrQuietBottom = 1
)

// QR renders text as a QR code that scanners read reliably in any terminal:
// dark modules on a light background, two modules per character cell.
// Uniform cells are drawn as coloured spaces, which fill the whole cell even
// with extra line spacing; only mixed cells use a half block with both
// foreground and background colour set.
//
// The half block is "▄" (background = top module, glyph = bottom module).
// Terminals that draw it from the font (Terminal.app) leave a strip of
// background above the glyph; with "▀" that strip showed the bottom module's
// colour and cut through the centre of the finder patterns. With "▄" the top
// strip has the right colour, and a strip below the glyph only narrows the
// light rows of the finder patterns, which land on bottom halves.
func QR(text string) string {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return ""
	}
	dark := func(x, y int) bool {
		x, y = x-qrQuietSide, y-qrQuietTop
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	width := code.Size + 2*qrQuietSide
	height := code.Size + qrQuietTop + qrQuietBottom

	if lipgloss.ColorProfile() == termenv.Ascii {
		return qrPlain(width, height, dark)
	}

	black, white := lipgloss.Color("0"), lipgloss.Color("15")
	colour := func(d bool) lipgloss.Color {
		if d {
			return black
		}
		return white
	}
	cell := map[[2]bool]string{}
	for _, top := range []bool{false, true} {
		for _, bottom := range []bool{false, true} {
			if top == bottom {
				cell[[2]bool{top, bottom}] = lipgloss.NewStyle().Background(colour(top)).Render(" ")
				continue
			}
			cell[[2]bool{top, bottom}] = lipgloss.NewStyle().Foreground(colour(bottom)).Background(colour(top)).Render("▄")
		}
	}
	var b strings.Builder
	for y := 0; y < height; y += 2 {
		for x := 0; x < width; x++ {
			b.WriteString(cell[[2]bool{dark(x, y), dark(x, y+1)}])
		}
		if y+2 < height {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// qrPlain is the colourless fallback (NO_COLOR): light modules are drawn
// with block characters, dark ones are left blank.
func qrPlain(width, height int, dark func(x, y int) bool) string {
	var b strings.Builder
	for y := 0; y < height; y += 2 {
		for x := 0; x < width; x++ {
			top, bottom := !dark(x, y), !dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteString(" ")
			}
		}
		if y+2 < height {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// CheckoutBlock shows a QR code (optional) with a short explanation and the
// checkout URL on its own line, so terminals keep it clickable.
func CheckoutBlock(url string, lines []string, withQR bool) string {
	urlLine := " " + Label.Render("Checkout URL") + "  " + Link.Render(url)
	if !withQR {
		return urlLine
	}
	qr := QR(url)
	text := strings.Join(lines, "\n")
	block := lipgloss.JoinHorizontal(lipgloss.Center, qr, "   ", text)
	if Width() < lipgloss.Width(block)+2 {
		block = qr
	}
	return IndentLines(block, " ") + "\n\n" + urlLine
}
