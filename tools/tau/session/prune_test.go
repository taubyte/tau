package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneSessions(t *testing.T) {
	tmp := t.TempDir()
	touch := func(name string, age time.Duration) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, nil, 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := touch("tau-session-1-2.yaml", 2*sessionMaxAge)
	fresh := touch("tau-session-3-4.yaml", time.Hour)
	other := touch("unrelated.yaml", 2*sessionMaxAge)

	pruneSessions(tmp, time.Now())

	if _, err := os.Stat(old); err == nil {
		t.Fatal("kept a stale session")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("removed %s", p)
		}
	}
}

func TestTestsStayOffRealSessions(t *testing.T) {
	if sessionTempDirOverride != "" {
		t.Skip("overridden")
	}
	if base := sessionBaseDir(); base == os.TempDir() {
		t.Fatalf("test binary uses the real session folder %s", base)
	}
}
