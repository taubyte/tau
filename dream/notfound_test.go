package dream

import (
	"errors"
	"strings"
	"testing"
)

// A wrong universe name said only "universe not found", which hid the usual
// cause: the other name (blackhole vs default). The error lists what exists.
func TestUniverseNotFoundListsTheOthers(t *testing.T) {
	m := lockTestMultiverse(t, "blackhole", "w78")

	_, err := m.Universe("default")
	var nf *UniverseNotFoundError
	if !errors.As(err, &nf) || nf.Name != "default" || strings.Join(nf.Existing, ",") != "blackhole,w78" {
		t.Fatalf("Universe: %v", err)
	}
	if !strings.Contains(err.Error(), "blackhole, w78") {
		t.Fatalf("message hides the universes there are: %q", err)
	}

	if err := m.Delete("default"); !errors.As(err, &nf) {
		t.Fatalf("Delete: %v", err)
	}

	empty := lockTestMultiverse(t)
	if _, err := empty.Universe("x"); err == nil || !strings.Contains(err.Error(), "dream new universe x") {
		t.Fatalf("empty: %v", err)
	}
}
