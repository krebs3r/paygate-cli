// Package i18n translates the full-screen app between English and German.
package i18n

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Lang is a supported interface language.
type Lang string

const (
	EN Lang = "en"
	DE Lang = "de"
)

var (
	mu      sync.RWMutex
	current = EN
)

// Set switches the language.
func Set(l Lang) {
	mu.Lock()
	defer mu.Unlock()
	if l != DE {
		l = EN
	}
	current = l
}

// Current returns the active language.
func Current() Lang {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Detect resolves a setting ("en", "de", "auto" or "") to a language. "auto"
// looks at LC_ALL, LC_MESSAGES and LANG, and on macOS at the system language.
func Detect(setting string) Lang {
	switch strings.ToLower(setting) {
	case "de":
		return DE
	case "en":
		return EN
	}
	for _, v := range []string{os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANG")} {
		if v == "" || v == "C" || v == "POSIX" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), "de") {
			return DE
		}
		return EN
	}
	if runtime.GOOS == "darwin" {
		if out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
			s := strings.TrimLeft(string(out), "( \n\t\"")
			if strings.HasPrefix(strings.ToLower(s), "de") {
				return DE
			}
		}
	}
	return EN
}

// T returns the text for key in the current language. Unknown keys are
// returned unchanged, so missing translations stay visible but harmless.
func T(key string) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	if Current() == DE && m.de != "" {
		return m.de
	}
	return m.en
}

// Tf formats a translated text.
func Tf(key string, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}
