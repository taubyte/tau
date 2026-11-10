package node

import (
	"slices"
	"testing"
)

func TestStartOrderPutsCatchAllsLast(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"auth", "tns", "hoarder", "seer", "substrate", "patrick", "monkey"}, []string{"auth", "tns", "hoarder", "seer", "patrick", "monkey", "substrate"}},
		{[]string{"gateway", "auth", "substrate", "patrick"}, []string{"auth", "patrick", "substrate", "gateway"}},
		{[]string{"gateway"}, []string{"gateway"}},
		{[]string{"auth"}, []string{"auth"}},
	} {
		if got := startOrder(c.in); !slices.Equal(got, c.want) {
			t.Errorf("startOrder(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
