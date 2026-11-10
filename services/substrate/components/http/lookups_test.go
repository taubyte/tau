package http

import (
	"testing"
	"time"
)

func TestStuckLookups(t *testing.T) {
	done := trackLookup()
	time.Sleep(20 * time.Millisecond)
	if n := StuckLookups(10 * time.Millisecond); n != 1 {
		t.Fatalf("a request in lookup past the bound: got %d, want 1", n)
	}
	if n := StuckLookups(time.Hour); n != 0 {
		t.Fatalf("a request within the bound counted: %d", n)
	}
	done() // lookup finished; the function may now run for hours
	if n := StuckLookups(0); n != 0 {
		t.Fatalf("a request past lookup still counted: %d", n)
	}
}
