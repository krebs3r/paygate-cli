package ui

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mdp/qrterminal/v3"
)

// QR renders text as a compact half-block QR code.
func QR(text string) string {
	var buf bytes.Buffer
	qrterminal.GenerateWithConfig(text, qrterminal.Config{
		Level:          qrterminal.L,
		Writer:         &buf,
		HalfBlocks:     true,
		BlackChar:      qrterminal.BLACK_BLACK,
		WhiteBlackChar: qrterminal.WHITE_BLACK,
		WhiteChar:      qrterminal.WHITE_WHITE,
		BlackWhiteChar: qrterminal.BLACK_WHITE,
		QuietZone:      1,
	})
	return strings.TrimRight(buf.String(), "\n")
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
