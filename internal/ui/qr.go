package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"rsc.io/qr"
)

// qrQuiet is the light border around the code in modules. One module on the
// dark terminal background scans reliably and keeps the code small.
const qrQuiet = 1

// QR renders text as a QR code that scanners read reliably in any terminal:
// dark modules on a light background, two modules per character cell. Each
// cell sets both foreground and background colour, so the code does not
// depend on the colour scheme and has no gaps with extra line spacing.
func QR(text string) string {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return ""
	}
	dark := func(x, y int) bool {
		x, y = x-qrQuiet, y-qrQuiet
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	size := code.Size + 2*qrQuiet

	if lipgloss.ColorProfile() == termenv.Ascii {
		return qrPlain(size, dark)
	}

	black, white := lipgloss.Color("0"), lipgloss.Color("15")
	cell := map[[2]bool]string{}
	for _, top := range []bool{false, true} {
		for _, bottom := range []bool{false, true} {
			fg, bg := white, white
			if top {
				fg = black
			}
			if bottom {
				bg = black
			}
			cell[[2]bool{top, bottom}] = lipgloss.NewStyle().Foreground(fg).Background(bg).Render("▀")
		}
	}
	var b strings.Builder
	for y := 0; y < size; y += 2 {
		for x := 0; x < size; x++ {
			b.WriteString(cell[[2]bool{dark(x, y), dark(x, y+1)}])
		}
		if y+2 < size {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// qrPlain is the colourless fallback (NO_COLOR): light modules are drawn
// with block characters, dark ones are left blank.
func qrPlain(size int, dark func(x, y int) bool) string {
	var b strings.Builder
	for y := 0; y < size; y += 2 {
		for x := 0; x < size; x++ {
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
		if y+2 < size {
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
