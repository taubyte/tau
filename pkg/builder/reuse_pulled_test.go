package builder

import "testing"

// A real cloud reuses a pulled image too: asking the registry before every
// build costs a round trip and counts against Docker Hub's pull limit.
func TestNodesReusePulledImages(t *testing.T) {
	if got := (&builder{dev: false}).reusePulled(); got <= 0 {
		t.Fatalf("non-dev node reuse window = %s, want > 0", got)
	}
	if got := (&builder{dev: true}).reusePulled(); got <= 0 {
		t.Fatalf("dev node reuse window = %s, want > 0", got)
	}
}
