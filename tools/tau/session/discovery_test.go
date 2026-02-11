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

	// pids must have length sessionAncestorDepth (6)
	pids := []int{100, 200, 300, 0, 0, 0} // closest-to-tau first, padded with 0
	ts := int64(1234567890123)
	got := sessionDirBaseName(pids, ts)
	want := "tau-test-session-1234567890123-0-0-0-300-200-100" // root first in name
	if got != want {
		t.Errorf("sessionDirBaseName(%v, %d) = %q; want %q", pids, ts, got, want)
	}

	// Wrong length returns empty
	if out := sessionDirBaseName([]int{1, 2}, ts); out != "" {
		t.Errorf("sessionDirBaseName(wrong length) = %q; want \"\"", out)
	}
	if out := sessionDirBaseName(make([]int, 10), ts); out != "" {
		t.Errorf("sessionDirBaseName(too long) = %q; want \"\"", out)
	}
}

func TestParseSessionDirBase(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// Valid format
	ts, pids, ok := parseSessionDirBase("tau-test-session-1234567890123-0-0-0-300-200-100")
	if !ok {
		t.Fatal("parseSessionDirBase: expected ok")
	}
	if ts != 1234567890123 {
		t.Errorf("timestamp = %d; want 1234567890123", ts)
	}
	wantPids := []int{0, 0, 0, 300, 200, 100}
	if len(pids) != len(wantPids) {
		t.Fatalf("pids length = %d; want %d", len(pids), len(wantPids))
	}
	for i := range wantPids {
		if pids[i] != wantPids[i] {
			t.Errorf("pids[%d] = %d; want %d", i, pids[i], wantPids[i])
		}
	}

	// Invalid prefix
	if _, _, ok := parseSessionDirBase("tau-session-1-0-0-0-0-0-0"); ok {
		t.Error("parseSessionDirBase(tau-session-...): expected !ok")
	}
	if _, _, ok := parseSessionDirBase("other-session-1-0-0-0-0-0-0"); ok {
		t.Error("parseSessionDirBase(other prefix): expected !ok")
	}

	// Wrong number of parts (need 1 ts + sessionAncestorDepth pids)
	if _, _, ok := parseSessionDirBase("tau-test-session-1-0-0-0"); ok {
		t.Error("parseSessionDirBase(too few parts): expected !ok")
	}
	if _, _, ok := parseSessionDirBase("tau-test-session-1-0-0-0-0-0-0-0"); ok {
		t.Error("parseSessionDirBase(too many parts): expected !ok")
	}

	// Non-numeric
	if _, _, ok := parseSessionDirBase("tau-test-session-abc-0-0-0-0-0-0"); ok {
		t.Error("parseSessionDirBase(non-numeric ts): expected !ok")
	}
	if _, _, ok := parseSessionDirBase("tau-test-session-1-0-0-0-0-0-x"); ok {
		t.Error("parseSessionDirBase(non-numeric pid): expected !ok")
	}
}

func TestParseSessionDirBaseRoundTrip(t *testing.T) {
	oldPrefix := sessionDirPrefix
	sessionDirPrefix = "tau-test"
	defer func() {
		sessionDirPrefix = oldPrefix
	}()

	// pids are [closest-to-tau, ..., root]; name stores root first, so parse returns [root,...,closest]
	pids := []int{100, 200, 300, 400, 500, 600}
	ts := int64(9876543210987)
	name := sessionDirBaseName(pids, ts)
	if name == "" {
		t.Fatal("sessionDirBaseName returned empty")
	}
	gotTs, gotPids, ok := parseSessionDirBase(name)
	if !ok {
		t.Fatalf("parseSessionDirBase(%q): expected ok", name)
	}
	if gotTs != ts {
		t.Errorf("round-trip timestamp = %d; want %d", gotTs, ts)
	}
	// parse returns order as in name: root first → [600,500,400,300,200,100]
	wantPids := []int{600, 500, 400, 300, 200, 100}
	for i := range wantPids {
		if gotPids[i] != wantPids[i] {
			t.Errorf("round-trip pids[%d] = %d; want %d", i, gotPids[i], wantPids[i])
		}
	}
	// sessionDirBaseName expects [closest,...,root]; reverse gotPids to get same name again
	reversed := make([]int, len(gotPids))
	for i := range gotPids {
		reversed[len(gotPids)-1-i] = gotPids[i]
	}
	if name2 := sessionDirBaseName(reversed, ts); name2 != name {
		t.Errorf("second round-trip name = %q; want %q", name2, name)
	}
}

func TestCurrentSessionPidListLength(t *testing.T) {
	pids := currentSessionPidList()
	if len(pids) != sessionAncestorDepth {
		t.Errorf("currentSessionPidList() length = %d; want %d", len(pids), sessionAncestorDepth)
	}
}

func TestEnsureExactSessionDirSwitchesWhenNeeded(t *testing.T) {
	Clear()
	defer Clear()

	tmp := t.TempDir()
	oldPrefix := sessionDirPrefix
	oldOverride := sessionTempDirOverride
	sessionDirPrefix = "tau-test"
	sessionTempDirOverride = tmp
	defer func() {
		sessionDirPrefix = oldPrefix
		sessionTempDirOverride = oldOverride
	}()

	// Create a dir that looks like a discovered session dir (suffix match style)
	// so ensureExactSessionDir will switch to the exact path for current process.
	suffixDir := filepath.Join(tmp, "tau-test-session-999-0-0-0-0-0-0")
	if err := os.MkdirAll(suffixDir, 0700); err != nil {
		t.Fatal(err)
	}
	err := LoadSessionInDir(suffixDir)
	if err != nil {
		t.Fatal(err)
	}
	err = Set().ProfileName("exact-test")
	if err != nil {
		t.Fatal(err)
	}
	// ensureExactSessionDir runs inside setKey; session is now in exact dir under tmp
	Clear()
	exactPath, err := discoverOrCreateConfigFileLoc()
	if err != nil {
		t.Fatal(err)
	}
	err = LoadSessionAt(exactPath)
	if err != nil {
		t.Fatal(err)
	}
	name, ok := Get().ProfileName()
	if !ok || name != "exact-test" {
		t.Errorf("ProfileName after switch: got %q, ok=%v; want \"exact-test\", true", name, ok)
	}
}
