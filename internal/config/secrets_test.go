package config

import "testing"

func TestCleanSecret(t *testing.T) {
	tests := map[string]string{
		"abc":             "abc",
		"  abc  ":         "abc",
		"abc\n":           "abc",
		"abc\r\n":         "abc",
		"\tabc\u00a0":     "abc",
		"\u200babc\ufeff": "abc",
		`"abc"`:           "abc",
		"'abc'":           "abc",
		`"abc`:            `"abc`,
		"a b":             "a b",
	}
	for in, want := range tests {
		if got := CleanSecret(in); got != want {
			t.Errorf("CleanSecret(%q) = %q, want %q", in, got, want)
		}
	}
}
