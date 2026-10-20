//go:build windows

package ui

import (
	"os"

	"github.com/muesli/termenv"
)

// Classic Windows consoles only interpret colours and cursor movement once
// virtual terminal processing is switched on. Windows Terminal ignores this.
func init() {
	_, _ = termenv.EnableVirtualTerminalProcessing(termenv.NewOutput(os.Stdout))
	_, _ = termenv.EnableVirtualTerminalProcessing(termenv.NewOutput(os.Stderr))
}
