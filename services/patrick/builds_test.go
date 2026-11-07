package service

import "testing"

func TestBuildSlots(t *testing.T) {
	var b buildSlots
	if !b.take("a1", "A", 1) || !b.take("b1", "B", 1) {
		t.Fatal("first build of each project must be given")
	}
	if b.take("a2", "A", 1) {
		t.Fatal("second build of A must wait")
	}
	if !b.take("a2", "A", 0) {
		t.Fatal("zero cap is unlimited")
	}
	b.release("a1")
	b.release("a2")
	if !b.take("a3", "A", 1) {
		t.Fatal("a finished build must free the slot")
	}
}
