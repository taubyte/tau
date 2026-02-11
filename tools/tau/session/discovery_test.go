package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestLongestCommonSuffixLength(t *testing.T) {
	// Suffix matching: compare from the end (leaf side)
	if got := longestCommonSuffixLength([]int{1, 2, 3, 4}, []int{5, 2, 3, 4}); got != 3 {
		t.Errorf("LCS([1,2,3,4], [5,2,3,4]) = %d; want 3", got)
	}
	if got := longestCommonSuffixLength([]int{1, 2}, []int{0, 1, 2}); got != 2 {
		t.Errorf("LCS([1,2], [0,1,2]) = %d; want 2", got)
	}
	if got := longestCommonSuffixLength([]int{1, 2}, []int{2, 1}); got != 0 {
		t.Errorf("LCS([1,2], [2,1]) = %d; want 0", got)
	}
	if got := longestCommonSuffixLength([]int{}, []int{1}); got != 0 {
		t.Errorf("LCS([], [1]) = %d; want 0", got)
	}
	// Identical lists
	if got := longestCommonSuffixLength([]int{10, 20, 30}, []int{10, 20, 30}); got != 3 {
		t.Errorf("LCS([10,20,30], [10,20,30]) = %d; want 3", got)
	}
	// Different root, same leaf
	if got := longestCommonSuffixLength([]int{100, 200, 300}, []int{999, 200, 300}); got != 2 {
		t.Errorf("LCS([100,200,300], [999,200,300]) = %d; want 2", got)
	}
}

func TestLongestCommonPrefixLength(t *testing.T) {
	if got := longestCommonPrefixLength([]int{1, 2, 3, 4}, []int{1, 2, 3, 5}); got != 3 {
		t.Errorf("LCP([1,2,3,4], [1,2,3,5]) = %d; want 3", got)
	}
	if got := longestCommonPrefixLength([]int{1, 2}, []int{1, 2, 3}); got != 2 {
		t.Errorf("LCP([1,2], [1,2,3]) = %d; want 2", got)
	}
	if got := longestCommonPrefixLength([]int{1, 2}, []int{2, 1}); got != 0 {
		t.Errorf("LCP([1,2], [2,1]) = %d; want 0", got)
	}
	if got := longestCommonPrefixLength([]int{}, []int{1}); got != 0 {
		t.Errorf("LCP([], [1]) = %d; want 0", got)
	}
}

func TestAncestorPathFromRoot(t *testing.T) {
	P := ancestorPathFromRoot(maxAncestorDepthForPath)
	if len(P) > maxAncestorDepthForPath {
		t.Errorf("ancestorPathFromRoot length = %d; want <= %d", len(P), maxAncestorDepthForPath)
	}
	// Same process gives same path when called twice
	P2 := ancestorPathFromRoot(maxAncestorDepthForPath)
	if len(P) != len(P2) {
		t.Errorf("two calls: len %d vs %d", len(P), len(P2))
	}
	for i := range P {
		if P[i] != P2[i] {
			t.Errorf("two calls differ at index %d: %d vs %d", i, P[i], P2[i])
		}
	}
}

func TestNewFormatSessionDoesNotForkOnSetKey(t *testing.T) {
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

	// Discover creates a session file (path in filename)
	file1, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	err = LoadSessionAt(file1)
	if err != nil {
		t.Fatal(err)
	}
	// setKey triggers ensureExactSessionDir; for file-based it must be no-op
	err = Set().ProfileName("no-fork-test")
	if err != nil {
		t.Fatal(err)
	}
	// Only one session file under tmp
	matches, _ := filepath.Glob(filepath.Join(tmp, "tau-test-session-*.yaml"))
	if len(matches) != 1 {
		t.Errorf("expected exactly one session file under tmp; got %d: %v", len(matches), matches)
	}
	if _sessionDir != filepath.Dir(file1) {
		t.Errorf("_sessionDir should be unchanged after setKey: got %q", _sessionDir)
	}
}

func TestSessionDirBaseName(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// pids must have length sessionAncestorDepth (16)
	pids := make([]int, sessionAncestorDepth)
	pids[0] = 100
	pids[1] = 200
	pids[2] = 300
	// rest are 0

	ts := int64(1234567890123)
	got := sessionDirBaseName(pids, ts)
	if got == "" {
		t.Fatal("sessionDirBaseName returned empty for valid input")
	}
	if !strings.HasPrefix(got, "tau-test-session-1234567890123-") {
		t.Errorf("expected prefix tau-test-session-1234567890123-; got %s", got)
	}

	// Wrong length returns empty
	if out := sessionDirBaseName([]int{1, 2}, ts); out != "" {
		t.Errorf("sessionDirBaseName(wrong length) = %q; want \"\"", out)
	}
	if out := sessionDirBaseName(make([]int, 20), ts); out != "" {
		t.Errorf("sessionDirBaseName(too long) = %q; want \"\"", out)
	}
}

func TestParseSessionFileBase(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// Build a valid new-format name
	leafPIDs := make([]int, sessionAncestorDepth)
	leafPIDs[0] = 5000
	leafPIDs[1] = 3000
	leafPIDs[2] = 2500
	name := sessionDirBaseNameFromLeafPath(leafPIDs[:3])
	pids, ok := parseSessionFileBase(name)
	if !ok {
		t.Fatalf("parseSessionFileBase(%q): expected ok", name)
	}
	if pids[0] != 5000 || pids[1] != 3000 || pids[2] != 2500 {
		t.Errorf("parsed pids: got %v; want [5000, 3000, 2500, 0...]", pids)
	}

	// Invalid prefix
	if _, ok := parseSessionFileBase("other-session-1-0-0-0"); ok {
		t.Error("parseSessionFileBase(other prefix): expected !ok")
	}

	// Wrong number of parts
	if _, ok := parseSessionFileBase("tau-test-session-1-0-0-0"); ok {
		t.Error("parseSessionFileBase(too few parts): expected !ok")
	}
}

func TestParseSessionDirBase(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// New format: exactly sessionAncestorDepth parts
	parts := make([]string, sessionAncestorDepth)
	for i := range parts {
		parts[i] = "0"
	}
	parts[0] = "5000"
	parts[1] = "3000"
	name := "tau-test-session-" + strings.Join(parts, "-")
	ts, pids, ok := parseSessionDirBase(name)
	if !ok {
		t.Fatalf("parseSessionDirBase(%q): expected ok", name)
	}
	if ts != 0 {
		t.Errorf("expected timestamp=0 for new format; got %d", ts)
	}
	if pids[0] != 5000 || pids[1] != 3000 {
		t.Errorf("parsed pids: got %v", pids)
	}

	// Legacy format with timestamp
	ts2, pids2, ok2 := parseSessionDirBase("tau-test-session-1234567890123-100-200-300-400-500-600")
	if !ok2 {
		t.Fatal("parseSessionDirBase(legacy): expected ok")
	}
	if ts2 != 1234567890123 {
		t.Errorf("legacy timestamp = %d; want 1234567890123", ts2)
	}
	if len(pids2) != 6 || pids2[0] != 100 || pids2[5] != 600 {
		t.Errorf("legacy pids: got %v", pids2)
	}

	// Invalid prefix
	if _, _, ok := parseSessionDirBase("other-session-1-0-0-0-0-0-0"); ok {
		t.Error("parseSessionDirBase(other prefix): expected !ok")
	}

	// Non-numeric
	if _, _, ok := parseSessionDirBase("tau-test-session-abc-0-0-0-0-0-0"); ok {
		t.Error("parseSessionDirBase(non-numeric ts): expected !ok")
	}
}

func TestCurrentSessionPidListLength(t *testing.T) {
	pids := currentSessionPidList()
	if len(pids) != sessionAncestorDepth {
		t.Errorf("currentSessionPidList() length = %d; want %d", len(pids), sessionAncestorDepth)
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

	// Discover creates a file-based session
	file1, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	err = LoadSessionAt(file1)
	if err != nil {
		t.Fatal(err)
	}
	// setKey triggers ensureExactSessionDir; for file-based it must be no-op
	err = Set().ProfileName("persist-test")
	if err != nil {
		t.Fatal(err)
	}

	// Re-discover and verify the data is there
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

func TestSessionSuffixMatchDistinguishesSiblings(t *testing.T) {
	// Verify that two terminal tabs with the same root but different leaf PIDs
	// get different suffix match scores.
	tabA := []int{5000, 3000, 2500, 2000, 1500, 1000}
	tabB := []int{6000, 3000, 2500, 2000, 1500, 1000}

	// Stored session matches tab A (leaf-first)
	storedA := []int{5000, 3000, 2500, 2000, 1500, 1000}

	lcsA := longestCommonSuffixLength(tabA, storedA)
	lcsB := longestCommonSuffixLength(tabB, storedA)

	if lcsA != 6 {
		t.Errorf("tab A suffix match with stored A: got %d; want 6", lcsA)
	}
	if lcsB != 5 {
		t.Errorf("tab B suffix match with stored A: got %d; want 5", lcsB)
	}
	if lcsB >= lcsA {
		t.Errorf("tab B should have a lower match score than tab A; got A=%d, B=%d", lcsA, lcsB)
	}
}

func TestCleanStaleSessionFiles(t *testing.T) {
	tmp := t.TempDir()
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// Create a session file with a PID that definitely doesn't exist (999999999)
	parts := make([]string, sessionAncestorDepth)
	for i := range parts {
		parts[i] = "0"
	}
	parts[0] = "999999999" // leaf PID that is almost certainly dead
	staleName := "tau-test-session-" + strings.Join(parts, "-") + ".yaml"
	stalePath := filepath.Join(tmp, staleName)
	if err := os.WriteFile(stalePath, []byte("stale: true\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cleanStaleSessionFiles(tmp)

	if _, err := os.Stat(stalePath); err == nil {
		t.Errorf("stale session file should have been removed: %s", stalePath)
	}
}
