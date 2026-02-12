package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscovery(t *testing.T) {
	tmp := t.TempDir()
	oldPrefix := sessionDirPrefix
	oldOverride := sessionTempDirOverride
	oldRootOverride := sessionRootDirOverride
	sessionDirPrefix = "tau-test"
	sessionTempDirOverride = tmp
	sessionRootDirOverride = tmp
	defer func() {
		sessionDirPrefix = oldPrefix
		sessionTempDirOverride = oldOverride
		sessionRootDirOverride = oldRootOverride
	}()

	file1, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	file2, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	if file1 != file2 {
		t.Errorf("session file should be stable: got %s then %s", file1, file2)
	}
	if filepath.Dir(file1) != tmp {
		t.Errorf("session file should be under test TempDir: %s", file1)
	}
	base := filepath.Base(file1)
	if !strings.HasPrefix(base, "tau-test-session-") || !strings.HasSuffix(base, ".yaml") {
		t.Errorf("session file name should match tau-test-session-*.yaml: got %s", base)
	}
}

func TestPidSetIntersection(t *testing.T) {
	if got := pidSetIntersection([]int{1, 2, 3, 4}, []int{5, 2, 3, 4}); got != 3 {
		t.Errorf("pidSetIntersection([1,2,3,4], [5,2,3,4]) = %d; want 3", got)
	}
	if got := pidSetIntersection([]int{1, 2}, []int{0, 1, 2}); got != 2 {
		t.Errorf("pidSetIntersection([1,2], [0,1,2]) = %d; want 2", got)
	}
	if got := pidSetIntersection([]int{1, 2}, []int{2, 1}); got != 2 {
		t.Errorf("pidSetIntersection([1,2], [2,1]) = %d; want 2 (set, order irrelevant)", got)
	}
	if got := pidSetIntersection([]int{}, []int{1}); got != 0 {
		t.Errorf("pidSetIntersection([], [1]) = %d; want 0", got)
	}
	if got := pidSetIntersection([]int{10, 20, 30}, []int{10, 20, 30}); got != 3 {
		t.Errorf("pidSetIntersection([10,20,30], [10,20,30]) = %d; want 3", got)
	}
	if got := pidSetIntersection([]int{100, 200, 300}, []int{999, 200, 300}); got != 2 {
		t.Errorf("pidSetIntersection([100,200,300], [999,200,300]) = %d; want 2", got)
	}
	if got := pidSetIntersection([]int{1, 2, 3}, []int{4, 5, 6}); got != 0 {
		t.Errorf("pidSetIntersection([1,2,3], [4,5,6]) = %d; want 0", got)
	}
}

func TestAncestorPathFromRoot(t *testing.T) {
	P := ancestorPathFromRoot(sessionMaxAncestors)
	if len(P) > sessionMaxAncestors {
		t.Errorf("ancestorPathFromRoot length = %d; want <= %d", len(P), sessionMaxAncestors)
	}
	P2 := ancestorPathFromRoot(sessionMaxAncestors)
	if len(P) != len(P2) {
		t.Errorf("two calls: len %d vs %d", len(P), len(P2))
	}
	for i := range P {
		if P[i] != P2[i] {
			t.Errorf("two calls differ at index %d: %d vs %d", i, P[i], P2[i])
		}
	}
}

func TestNewFormatSessionForkOnSetKey(t *testing.T) {
	Clear()
	defer Clear()

	tmp := t.TempDir()
	oldPrefix := sessionDirPrefix
	oldOverride := sessionTempDirOverride
	oldRootOverride := sessionRootDirOverride
	sessionDirPrefix = "tau-test"
	sessionTempDirOverride = tmp
	sessionRootDirOverride = tmp
	defer func() {
		sessionDirPrefix = oldPrefix
		sessionTempDirOverride = oldOverride
		sessionRootDirOverride = oldRootOverride
	}()

	file1, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	err = LoadSessionAt(file1)
	if err != nil {
		t.Fatal(err)
	}
	err = Set().ProfileName("fork-test")
	if err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(tmp, "tau-test-session-*.yaml"))
	if len(matches) < 1 {
		t.Errorf("expected at least one session file under tmp; got %d: %v", len(matches), matches)
	}
	if _sessionDir != filepath.Dir(file1) {
		t.Errorf("_sessionDir should be unchanged after setKey: got %q", _sessionDir)
	}
}

func TestSessionFileBaseName(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	pids := []int{100, 200, 300}
	got := sessionFileBaseName(pids)
	if got == "" {
		t.Fatal("sessionFileBaseName returned empty for valid input")
	}
	if !strings.HasPrefix(got, "tau-test-session-") {
		t.Errorf("expected prefix tau-test-session-; got %s", got)
	}
	if got != "tau-test-session-100-200-300" {
		t.Errorf("sessionFileBaseName([100,200,300]) = %q; want tau-test-session-100-200-300", got)
	}

	if sessionFileBaseName([]int{}) != "" {
		t.Errorf("sessionFileBaseName([]) should return empty")
	}
}

func TestParseSessionPIDs(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	name := "tau-test-session-5000-3000-2500"
	pids, ok := parseSessionPIDs(name)
	if !ok {
		t.Fatalf("parseSessionPIDs(%q): expected ok", name)
	}
	if len(pids) != 3 || pids[0] != 5000 || pids[1] != 3000 || pids[2] != 2500 {
		t.Errorf("parsed pids: got %v; want [5000, 3000, 2500]", pids)
	}

	if _, ok := parseSessionPIDs("other-session-1-0-0"); ok {
		t.Error("parseSessionPIDs(other prefix): expected !ok")
	}

	if _, ok := parseSessionPIDs("tau-test-session-"); ok {
		t.Error("parseSessionPIDs(empty rest): expected !ok")
	}

	if _, ok := parseSessionPIDs("tau-test-session-abc"); ok {
		t.Error("parseSessionPIDs(non-numeric): expected !ok")
	}
}

func TestParseLegacySessionPIDs(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// Old fixed-length format: 16 parts
	parts := make([]string, 16)
	for i := range parts {
		parts[i] = "0"
	}
	parts[0] = "5000"
	parts[1] = "3000"
	name := "tau-test-session-" + strings.Join(parts, "-")
	pids2, ok2 := parseLegacySessionPIDs(name)
	if !ok2 {
		t.Fatalf("parseLegacySessionPIDs(%q): expected ok", name)
	}
	if pids2[0] != 5000 || pids2[1] != 3000 {
		t.Errorf("parsed pids: got %v", pids2)
	}

	if _, ok := parseLegacySessionPIDs("other-session-1-0-0-0-0-0-0"); ok {
		t.Error("parseLegacySessionPIDs(other prefix): expected !ok")
	}

	if _, ok := parseLegacySessionPIDs("tau-test-session-abc-0-0-0-0-0-0"); ok {
		t.Error("parseLegacySessionPIDs(invalid): expected !ok")
	}
}

func TestDiscoveryPreferNewestOnTie(t *testing.T) {
	Clear()
	defer Clear()

	tmp := t.TempDir()
	oldPrefix := sessionDirPrefix
	oldOverride := sessionTempDirOverride
	oldRootOverride := sessionRootDirOverride
	sessionDirPrefix = "tau-test"
	sessionTempDirOverride = tmp
	sessionRootDirOverride = tmp
	defer func() {
		sessionDirPrefix = oldPrefix
		sessionTempDirOverride = oldOverride
		sessionRootDirOverride = oldRootOverride
	}()

	pids := ancestorPIDs(sessionMaxAncestors)
	if len(pids) < 2 {
		t.Skip("need at least 2 PIDs for tie test")
	}

	root := sessionRootDir()
	base1 := sessionFileBaseName(pids[0:1])
	base2 := sessionFileBaseName(pids[1:2])
	path1 := filepath.Join(root, base1+".yaml")
	path2 := filepath.Join(root, base2+".yaml")

	if err := os.WriteFile(path1, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path2, time.Now(), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	got, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(path2)
	if filepath.Clean(got) != want {
		t.Errorf("discoverOrCreateConfigFileLoc() = %q; want %q (newest file on tie)", got, want)
	}
}

func TestFileBasedSessionPersistsAcrossSetKey(t *testing.T) {
	Clear()
	defer Clear()

	tmp := t.TempDir()
	oldPrefix := sessionDirPrefix
	oldOverride := sessionTempDirOverride
	oldRootOverride := sessionRootDirOverride
	sessionDirPrefix = "tau-test"
	sessionTempDirOverride = tmp
	sessionRootDirOverride = tmp
	defer func() {
		sessionDirPrefix = oldPrefix
		sessionTempDirOverride = oldOverride
		sessionRootDirOverride = oldRootOverride
	}()

	file1, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	err = LoadSessionAt(file1)
	if err != nil {
		t.Fatal(err)
	}
	err = Set().ProfileName("persist-test")
	if err != nil {
		t.Fatal(err)
	}

	Clear()
	file2, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	if file1 != file2 {
		t.Errorf("session file should be stable: got %s then %s", file1, file2)
	}
	err = LoadSessionAt(file2)
	if err != nil {
		t.Fatal(err)
	}
	name, ok := Get().ProfileName()
	if !ok || name != "persist-test" {
		t.Errorf("ProfileName after re-discover: got %q, ok=%v; want \"persist-test\", true", name, ok)
	}
}

func TestPidSetIntersectionDistinguishesSiblings(t *testing.T) {
	tabA := []int{5000, 3000, 2500, 2000, 1500, 1000}
	tabB := []int{6000, 3000, 2500, 2000, 1500, 1000}
	storedA := []int{5000, 3000, 2500, 2000, 1500, 1000}

	interA := pidSetIntersection(tabA, storedA)
	interB := pidSetIntersection(tabB, storedA)

	if interA != 6 {
		t.Errorf("tab A intersection with stored A: got %d; want 6", interA)
	}
	if interB != 5 {
		t.Errorf("tab B intersection with stored A: got %d; want 5", interB)
	}
	if interB >= interA {
		t.Errorf("tab B should have lower intersection than tab A; got A=%d, B=%d", interA, interB)
	}
}
