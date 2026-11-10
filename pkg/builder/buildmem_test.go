package builder

import "testing"

func TestBuildMemoryLeavesRoomOnSmallNodes(t *testing.T) {
	const gib = 1 << 30
	cases := map[uint64]int64{
		0:          4 * gib, // unknown
		16 * gib:   4 * gib,
		5 * gib:    4 * gib,
		2 * gib:    1 * gib, // a 2 GB node keeps 1 GiB for tau and Docker
		1200 << 20: 512 << 20,
	}
	for total, want := range cases {
		if got := buildMemory(total); got != want {
			t.Errorf("buildMemory(%d) = %d, want %d", total, got, want)
		}
	}
}

func TestBuildSwap(t *testing.T) {
	const gib = 1 << 30
	cases := map[uint64]int64{
		0:         4 * gib, // unknown size: the default, no swap
		2 * gib:   -1,
		5*gib - 1: -1,
		5 * gib:   4 * gib,
		32 * gib:  4 * gib,
	}
	for total, want := range cases {
		if got := buildSwap(buildMemory(total)); got != want {
			t.Errorf("buildSwap at total %d = %d, want %d", total, got, want)
		}
	}
}
