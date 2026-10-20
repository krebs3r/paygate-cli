package money

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in, cur string
		want    int64
		wantErr bool
	}{
		{"12.99", "EUR", 1299, false},
		{"12,99", "eur", 1299, false},
		{"12", "EUR", 1200, false},
		{"0.5", "EUR", 50, false},
		{"1000", "JPY", 1000, false},
		{"1.5", "JPY", 0, true},
		{"1.234", "KWD", 1234, false},
		{"1.999", "EUR", 0, true},
		{"0", "EUR", 0, true},
		{"-1", "EUR", 0, true},
		{"abc", "EUR", 0, true},
		{"", "EUR", 0, true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in, tt.cur)
		if (err != nil) != tt.wantErr {
			t.Errorf("Parse(%q, %s) error = %v, wantErr %v", tt.in, tt.cur, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("Parse(%q, %s) = %d, want %d", tt.in, tt.cur, got, tt.want)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		minor int64
		cur   string
		want  string
	}{
		{1299, "EUR", "12.99 EUR"},
		{5, "eur", "0.05 EUR"},
		{1000, "JPY", "1000 JPY"},
		{1234, "KWD", "1.234 KWD"},
		{-250, "EUR", "-2.50 EUR"},
	}
	for _, tt := range tests {
		if got := Format(tt.minor, tt.cur); got != tt.want {
			t.Errorf("Format(%d, %s) = %q, want %q", tt.minor, tt.cur, got, tt.want)
		}
	}
}
