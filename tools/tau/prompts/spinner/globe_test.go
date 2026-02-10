package spinner

import (
	"testing"
)

func TestGlobe(t *testing.T) {
	stop := Globe()
	if stop == nil {
		t.Fatal("Globe() returned nil stop")
	}
	stop()
}
