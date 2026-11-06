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

func TestDCCCards(t *testing.T) {
	luhn := func(n string) bool {
		sum := 0
		for i := len(n) - 1; i >= 0; i-- {
			d := int(n[i] - '0')
			if (len(n)-i)%2 == 0 {
				if d *= 2; d > 9 {
					d -= 9
				}
			}
			sum += d
		}
		return sum%10 == 0
	}
	seen := map[string]bool{}
	for _, c := range DCCCards {
		if !luhn(c.Number) || len(c.Number) != 16 || DetectBrand(c.Number) != c.Brand || seen[c.Currency] {
			t.Errorf("eDCC card %+v: luhn %v, brand %q", c, luhn(c.Number), DetectBrand(c.Number))
		}
		seen[c.Currency] = true
		if card, ok := FindCard(c.Alias); !ok || card.ThreeDS || card.Number != c.Number || !IsDCCBIN(card.Number) {
			t.Errorf("FindCard(%s) = %+v, %v", c.Alias, card, ok)
		}
	}
	if IsDCCBIN("4111111111111111") || !IsDCCBIN("4761730000166317") || !IsDCCBIN("4761 7300 00") {
		t.Error("IsDCCBIN must know the guide's BINs, also those without an alias, and nothing else")
	}
	if bin, err := BIN("4176 6622 8061 3305"); err != nil || bin != "4176662280" {
		t.Errorf("BIN = %q, %v", bin, err)
	}
	if _, err := BIN("41766622"); err == nil {
		t.Error("8 digits are too few")
	}
}

func TestIsTestBIN(t *testing.T) {
	for number, want := range map[string]bool{
		"4176662280":          true,  // eDCC test card
		"4111111111111111":    true,  // visa-ok
		"3750000000":          true,  // amex-ok, 15 digits
		"5300000000":          false, // no test card
		"4111111111111111111": true,
		"411111":              false, // too short for a BIN
	} {
		if got := IsTestBIN(number); got != want {
			t.Errorf("IsTestBIN(%s) = %v, want %v", number, got, want)
		}
	}
}

func TestDCCTestCards(t *testing.T) {
	luhn := func(n string) bool {
		sum := 0
		for i := len(n) - 1; i >= 0; i-- {
			d := int(n[i] - '0')
			if (len(n)-i)%2 == 0 {
				if d *= 2; d > 9 {
					d -= 9
				}
			}
			sum += d
		}
		return sum%10 == 0
	}
	if len(DCCTestCards) != 28 {
		t.Fatalf("the test guide lists 28 cards, got %d", len(DCCTestCards))
	}
	seen := map[string]bool{}
	for _, c := range DCCTestCards {
		if len(c.BIN) != 10 || len(c.Number) != 16 || c.Number[:10] != c.BIN || !luhn(c.Number) || len(c.Currency) != 3 || seen[c.BIN] {
			t.Errorf("test card %+v", c)
		}
		seen[c.BIN] = true
		if !IsDCCBIN(c.BIN) || !IsDCCBIN(c.Number) {
			t.Errorf("IsDCCBIN(%s) is false", c.BIN)
		}
	}
	for _, d := range DCCCards {
		if c, ok := FindDCCTestCard(d.Number); !ok || c.Number != d.Number || c.Currency != d.Currency || c.Brand() != d.Brand {
			t.Errorf("alias %s is not a row of the table: %+v", d.Alias, c)
		}
	}
	if b := (DCCTestCard{Number: "6799998900558922"}).Brand(); b != "" {
		t.Errorf("6799… has no known brand, got %q", b)
	}
}
