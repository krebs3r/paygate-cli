package ui

import (
	"fmt"
	"sync"
	"time"
)

var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Spin shows an animated spinner on stderr while fn runs. Without a TTY it
// prints nothing, so piped output stays clean.
func Spin(msg string, fn func() error) error {
	if !StderrIsTTY() {
		return fn()
	}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			fmt.Fprintf(Stderr, "\r\033[2K %s %s", Accent.Render(spinnerFrames[i%len(spinnerFrames)]), Label.Render(msg))
			i++
			select {
			case <-done:
				fmt.Fprint(Stderr, "\r\033[2K")
				return
			case <-t.C:
			}
		}
	}()
	err := fn()
	close(done)
	wg.Wait()
	return err
}
