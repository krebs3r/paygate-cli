package cards

import "testing"

func TestFindCardAndBrand(t *testing.T) {
	c, ok := FindCard("VISA-OK")
	if !ok || c.Number != "4111111111111111" {
		t.Fatalf("FindCard(VISA-OK) = %+v, %v", c, ok)
	}
	for _, c := range Cards {
		if got := DetectBrand(c.Number); got != c.Brand {
			t.Errorf("DetectBrand(%s) = %q, want %q", c.Number, got, c.Brand)
		}
	}
	if got := Mask("4111111111111111"); got != "•••• 1111" {
		t.Errorf("Mask = %q", got)
	}
}
