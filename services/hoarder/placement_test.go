//go:build !ee

package hoarder

import (
	"fmt"
	"reflect"
	"testing"
)

func TestPlacementDesired_Deterministic(t *testing.T) {
	members := []string{"a", "b", "c", "d"}
	got := placementDesired("resource-1", members, 2)
	// Order-independent: every node computes the same owners from the same set.
	if !reflect.DeepEqual(got, placementDesired("resource-1", []string{"d", "c", "b", "a"}, 2)) {
		t.Fatalf("HRW must be order-independent, got %v", got)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 owners, got %v", got)
	}
}

func TestPlacementDesired_ClampAndEmpty(t *testing.T) {
	if got := placementDesired("r", []string{"a"}, 3); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("target clamps to fleet: %v", got)
	}
	if got := placementDesired("r", nil, 2); got != nil {
		t.Fatalf("no members -> nil: %v", got)
	}
	if got := placementDesired("r", []string{"a", "b"}, 0); got != nil {
		t.Fatalf("target 0 -> nil: %v", got)
	}
}

// HRW's defining property: dropping a member only re-homes the resources that
// member owned. Everything else stays put — cheap, low-thrash re-placement.
func TestPlacementDesired_MinimalMovementOnMemberLoss(t *testing.T) {
	members := []string{"a", "b", "c", "d", "e"}
	remaining := []string{"a", "b", "d", "e"} // drop c
	for i := 0; i < 50; i++ {
		r := fmt.Sprintf("res-%d", i)
		before := placementDesired(r, members, 2)
		after := placementDesired(r, remaining, 2)
		if !contains(before, "c") && !reflect.DeepEqual(before, after) {
			t.Fatalf("res %s churned owners without owning c: %v -> %v", r, before, after)
		}
	}
}

func TestTargetReplicas_ClampsToFleet(t *testing.T) {
	cases := []struct{ fleet, want int }{
		{0, 1}, // never below 1
		{1, 1}, // single-node cloud
		{2, 2}, // clamps to fleet
		{3, 3}, // default target
		{9, 3}, // capped at default
	}
	for _, c := range cases {
		if got := targetReplicas(c.fleet); got != c.want {
			t.Errorf("targetReplicas(%d) = %d, want %d", c.fleet, got, c.want)
		}
	}
}
