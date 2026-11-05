package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A stale session file can hold a PID the OS has since reused for one of this
// process's ancestors. That accident must not beat the file that really shares
// this process's chain, or commands silently land on another cloud or project.
func TestDiscoveryIgnoresReusedPID(t *testing.T) {
	leaf := ancestorPIDs(sessionMaxAncestors)
	if len(leaf) < 3 {
		t.Skip("needs at least three ancestors")
	}

	tmp := t.TempDir()
	old := sessionRootDirOverride
	sessionRootDirOverride = tmp
	defer func() { sessionRootDirOverride = old }()

	write := func(pids []int, age time.Duration) string {
		p := filepath.Join(tmp, sessionFileBaseName(pids)+".yaml")
		if err := os.WriteFile(p, nil, 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}

	right := write(append([]int{70001}, leaf[1:]...), time.Hour)
	// as many PIDs in common and newer, but its copy of leaf[0] is a coincidence
	write(append([]int{leaf[0], 70002}, leaf[2:]...), time.Minute)

	got, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	if got != right {
		t.Fatalf("chose %s\nwant %s", filepath.Base(got), filepath.Base(right))
	}
}

func TestLongestCommonRun(t *testing.T) {
	for _, c := range []struct {
		a, b []int
		want int
	}{
		{[]int{1, 2, 3, 4}, []int{5, 2, 3, 4}, 3},
		{[]int{1, 2, 3, 4}, []int{1, 9, 3, 4}, 2},
		{[]int{1, 2}, []int{2, 1}, 1},
		{[]int{1, 2}, []int{3, 4}, 0},
		{[]int{0, 0}, []int{0, 0}, 0},
		{nil, []int{1}, 0},
		// chains cut at different depths still match
		{[]int{1, 2, 3, 4}, []int{9, 8, 2, 3}, 2},
	} {
		if got := longestCommonRun(c.a, c.b); got != c.want {
			t.Errorf("longestCommonRun(%v, %v) = %d; want %d", c.a, c.b, got, c.want)
		}
	}
}
