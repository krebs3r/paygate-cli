package ui

import "testing"

func TestSetTheme(t *testing.T) {
	defer SetTheme("")
	SetTheme("blurple")
	if ThemeID() != "blurple" || ColAccent != Themes[1].Accent || Muted.GetForeground() != Themes[1].Muted {
		t.Fatalf("blurple not applied: %s %v", ThemeID(), ColAccent)
	}
	if logoGradient[5] != Themes[1].Logo[5] {
		t.Error("the logo must follow the theme")
	}
	if ColSuccess != statusSuccess {
		t.Error("status colours stay the same in every theme")
	}
	SetTheme("does-not-exist")
	if ThemeID() != "blue" || ColAccent != Themes[0].Accent {
		t.Errorf("an unknown theme must fall back to the default, got %s", ThemeID())
	}
	if Swatch("blurple") == "" || Swatch("nope") != "" {
		t.Error("swatch")
	}
}
