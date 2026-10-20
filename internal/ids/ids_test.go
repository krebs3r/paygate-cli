package ids

import (
	"regexp"
	"testing"
)

func TestNewTransIDIsIdealSafe(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9]{1,24}$`)
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		id := NewTransID()
		if !re.MatchString(id) {
			t.Fatalf("transId %q is not alphanumeric with max. 24 characters", id)
		}
		if seen[id] {
			t.Fatalf("duplicate transId %q", id)
		}
		seen[id] = true
	}
}

func TestIdempotencyKeysDiffer(t *testing.T) {
	if NewIdempotencyKey() == NewIdempotencyKey() {
		t.Fatal("idempotency keys must be random")
	}
}
