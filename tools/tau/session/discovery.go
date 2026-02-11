package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mitchellh/go-ps"
)

func debugEnabled() bool { return os.Getenv("DEBUG") == "1" }

func debugSession(format string, args ...interface{}) {
	if debugEnabled() {
		fmt.Fprintf(os.Stderr, "[tau session] "+format+"\n", args...)
	}
}

const debugProcessTreeMaxDepth = 20

// debugProcessTree dumps the full process ancestry from getppid() to root (pid 1 or unknown).
func debugProcessTree() {
	if !debugEnabled() {
		return
	}
	pid := os.Getppid()
	debugSession("--- process tree (tau's parent = %d), max depth %d ---", pid, debugProcessTreeMaxDepth)
	depth := 0
	seen := make(map[int]bool)
	for pid > 0 && !seen[pid] && depth <= debugProcessTreeMaxDepth {
		seen[pid] = true
		process, err := ps.FindProcess(pid)
		if err != nil {
			debugSession("  [%2d] pid=%d FindProcess err=%v", depth, pid, err)
			break
		}
		if process == nil {
			debugSession("  [%2d] pid=%d ppid=? exe=%q (FindProcess => nil, trying processName)", depth, pid, processName(pid))
			break
		}
		exe := process.Executable()
		ppid := process.PPid()
		tags := ""
		if isRunner(exe) {
			tags = " [runner]"
		}
		if isShell(exe) {
			tags += " [shell]"
		}
		debugSession("  [%2d] pid=%d ppid=%d exe=%q%s", depth, process.Pid(), ppid, exe, tags)
		if ppid == 0 || ppid == 1 {
			debugSession("  [%2d] (root)", depth+1)
			break
		}
		pid = ppid
		depth++
	}
	if depth > debugProcessTreeMaxDepth {
		debugSession("  ... (stopped at max depth %d)", debugProcessTreeMaxDepth)
	}
	debugSession("--- end process tree ---")
	debugProcessTreeNative(os.Getppid())
}

// executableBase returns the executable name without path and without .exe (for Windows).
func executableBase(exe string) string {
	base := filepath.Base(exe)
	base = strings.TrimSuffix(base, ".exe")
	return strings.ToLower(base)
}

func isRunner(exe string) bool {
	b := executableBase(exe)
	return b == "go" || b == "node"
}

func isShell(exe string) bool {
	b := executableBase(exe)
	switch b {
	case "bash", "sh", "zsh", "dash", "fish", "cmd", "powershell", "pwsh", "mintty":
		return true
	}
	return false
}

// processName returns the executable name for pid.
func processName(pid int) string {
	p, err := ps.FindProcess(pid)
	if err != nil || p == nil {
		if name := getProcessNameNative(pid); name != "" {
			return name
		}
		return "?"
	}
	return p.Executable()
}

// ancestorPIDs returns the list of ancestor PIDs from tau's parent upward, up to maxDepth.
// p0 = getppid(), p1 = parent(p0), etc. (leaf-first: closest-to-tau first)
func ancestorPIDs(maxDepth int) []int {
	pid := os.Getppid()
	out := make([]int, 0, maxDepth)
	for i := 0; i < maxDepth && pid > 0 && pid != 1; i++ {
		out = append(out, pid)
		next := 0
		process, err := ps.FindProcess(pid)
		if err != nil || process == nil {
			next, _ = getParentPIDNative(pid)
		} else {
			next = process.PPid()
		}
		if next == 0 || next == 1 {
			break
		}
		pid = next
	}
	return out
}

// ancestorPathFromRoot returns the ancestor chain from root to tau's parent (root-first), up to maxDepth.
func ancestorPathFromRoot(maxDepth int) []int {
	pids := ancestorPIDs(maxDepth)
	for i, j := 0, len(pids)-1; i < j; i, j = i+1, j-1 {
		pids[i], pids[j] = pids[j], pids[i]
	}
	return pids
}

// longestCommonSuffixLength returns the length of the longest common suffix of a and b (element-wise from the end).
// This compares from the leaf side (closest-to-tau), which is the most distinguishing part of the tree.
func longestCommonSuffixLength(a, b []int) int {
	la, lb := len(a), len(b)
	n := la
	if lb < n {
		n = lb
	}
	count := 0
	for i := 0; i < n; i++ {
		if a[la-1-i] != b[lb-1-i] {
			break
		}
		count++
	}
	return count
}

// longestCommonPrefixLength is kept for legacy compatibility and tests.
func longestCommonPrefixLength(a, b []int) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// trimLeadingZeros returns a slice without leading zero elements (legacy dir names pad with 0 at root side).
func trimLeadingZeros(pids []int) []int {
	j := 0
	for j < len(pids) && pids[j] == 0 {
		j++
	}
	return pids[j:]
}

// reverseInts reverses a slice of ints in place.
func reverseInts(s []int) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// trimTrailingZeros returns a slice without trailing zero elements.
func trimTrailingZeros(pids []int) []int {
	j := len(pids)
	for j > 0 && pids[j-1] == 0 {
		j--
	}
	return pids[:j]
}

// sessionDirBaseNameFromLeafPath builds a session file base name from leaf-first PIDs.
// Stores the leaf-most (closest-to-tau) PIDs, padded with 0 on the right up to sessionAncestorDepth.
// Format: tau-session-p0-p1-...-p15 (leaf-first: p0=tau's parent, p15=root-most collected).
func sessionDirBaseNameFromLeafPath(leafFirstPIDs []int) string {
	stored := make([]int, sessionAncestorDepth)
	for i := 0; i < sessionAncestorDepth; i++ {
		if i < len(leafFirstPIDs) {
			stored[i] = leafFirstPIDs[i]
		}
	}
	parts := make([]string, sessionAncestorDepth)
	for i := range stored {
		parts[i] = strconv.Itoa(stored[i])
	}
	return sessionDirPrefix + "-session-" + strings.Join(parts, "-")
}

// sessionDirBaseName returns the base name for a session dir/file (legacy format with timestamp).
// pids is [p0,...,pk,0,...,0] (closest-to-tau first, padded right with 0). Components in name are root-first.
func sessionDirBaseName(pids []int, timestampMs int64) string {
	if len(pids) != sessionAncestorDepth {
		return ""
	}
	components := make([]string, len(pids))
	for i := range pids {
		components[len(pids)-1-i] = strconv.Itoa(pids[i])
	}
	return sessionDirPrefix + "-session-" + strconv.FormatInt(timestampMs, 10) + "-" + strings.Join(components, "-")
}

func sessionBaseDir() string {
	if sessionTempDirOverride != "" {
		return sessionTempDirOverride
	}
	return os.TempDir()
}

// sessionRootDir returns the single directory that contains session YAML files.
func sessionRootDir() string {
	if sessionRootDirOverride != "" {
		return sessionRootDirOverride
	}
	return filepath.Join(sessionBaseDir(), "tau")
}

// sessionDirFromPidList builds the session directory path for creation (uses current time in ms).
// Legacy format with timestamp for dir-based sessions.
func sessionDirFromPidList(pids []int) string {
	name := sessionDirBaseName(pids, time.Now().UnixMilli())
	if name == "" {
		return ""
	}
	return filepath.Join(sessionBaseDir(), name)
}

// parseSessionFileBase parses a new-format session file base name (no timestamp).
// Format: "tau-session-p0-p1-...-p15" (leaf-first PIDs, sessionAncestorDepth parts).
// Returns (pids, true) or (nil, false) if invalid.
func parseSessionFileBase(name string) (pids []int, ok bool) {
	prefix := sessionDirPrefix + "-session-"
	if !strings.HasPrefix(name, prefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(name, prefix)
	parts := strings.Split(rest, "-")
	if len(parts) != sessionAncestorDepth {
		return nil, false
	}
	out := make([]int, sessionAncestorDepth)
	for i := 0; i < sessionAncestorDepth; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// parseSessionDirBase parses a session file/dir base name into timestamp and pids (root-first).
// Accepts: "tau-session-<ts>-<p0>-...-<p5>" (legacy, 7 parts with timestamp) or
// "tau-session-<p0>-...-<p15>" (new format, sessionAncestorDepth parts, no timestamp).
// Returns (0, nil, false) if format is invalid.
func parseSessionDirBase(name string) (timestampMs int64, pids []int, ok bool) {
	prefix := sessionDirPrefix + "-session-"
	if !strings.HasPrefix(name, prefix) {
		return 0, nil, false
	}
	rest := strings.TrimPrefix(name, prefix)
	parts := strings.Split(rest, "-")

	// New format: exactly sessionAncestorDepth parts (no timestamp)
	if len(parts) == sessionAncestorDepth {
		out := make([]int, sessionAncestorDepth)
		for i := 0; i < sessionAncestorDepth; i++ {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				return 0, nil, false
			}
			out[i] = n
		}
		return 0, out, true
	}

	// Legacy format: timestamp + 6 PID parts (for old sessions with depth 6)
	// Try: first part is timestamp, rest are PIDs
	if len(parts) >= 2 {
		ts, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, nil, false
		}
		pidParts := parts[1:]
		out := make([]int, len(pidParts))
		for i, p := range pidParts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return 0, nil, false
			}
			out[i] = n
		}
		return ts, out, true
	}

	return 0, nil, false
}

// isProcessAlive checks if a process with the given PID is alive.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := ps.FindProcess(pid)
	if err != nil {
		return false
	}
	return p != nil
}

// cleanStaleSessionFiles removes session files where ALL non-zero stored PIDs are dead.
// This is conservative: a file is only removed when the entire process tree is gone,
// not just when the leaf process (which may be a short-lived wrapper like `go run`) exits.
func cleanStaleSessionFiles(root string) {
	pattern := filepath.Join(root, sessionDirPrefix+"-session-*.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, path := range matches {
		base := filepath.Base(path)
		baseNoExt := strings.TrimSuffix(base, ".yaml")
		pids, ok := parseSessionFileBase(baseNoExt)
		if !ok {
			continue
		}
		anyAlive := false
		for _, pid := range pids {
			if pid > 0 && isProcessAlive(pid) {
				anyAlive = true
				break
			}
		}
		if !anyAlive {
			debugSession("cleanup: removing stale session file=%q (all PIDs dead)", base)
			os.Remove(path)
		}
	}
}

// currentSessionPidList returns the current process ancestor list padded to sessionAncestorDepth.
// Leaf-first (closest-to-tau first), padded right with 0.
func currentSessionPidList() []int {
	pids := ancestorPIDs(sessionAncestorDepth)
	for len(pids) < sessionAncestorDepth {
		pids = append(pids, 0)
	}
	return pids
}

func discoverOrCreateConfigFileLoc() (string, error) {
	debugProcessTree()
	// Collect leaf-first PIDs (closest to tau first)
	leafPIDs := ancestorPIDs(maxAncestorDepthForPath)
	debugSession("discover: leaf-first PIDs len=%d P=%v threshold=%d", len(leafPIDs), leafPIDs, sessionCommonSuffixThreshold)

	root := sessionRootDir()

	// Clean stale session files before discovery
	cleanStaleSessionFiles(root)

	var bestFile string
	var bestL int

	// 1) New-format session YAML files (leaf-first PIDs in filename, no timestamp)
	pattern := filepath.Join(root, sessionDirPrefix+"-session-*.yaml")
	fileMatches, err := filepath.Glob(pattern)
	if err != nil {
		debugSession("discover: glob files err=%v", err)
	} else {
		for _, path := range fileMatches {
			info, err := os.Stat(path)
			if err != nil || info.IsDir() {
				continue
			}
			base := filepath.Base(path)
			baseNoExt := strings.TrimSuffix(base, ".yaml")
			storedPIDs, ok := parseSessionFileBase(baseNoExt)
			if !ok {
				// Try legacy format
				_, storedPIDs, ok = parseSessionDirBase(baseNoExt)
				if !ok {
					debugSession("discover: skip file=%q (parse failed)", base)
					continue
				}
				// Legacy stored PIDs are root-first; reverse to leaf-first for suffix matching
				storedPIDs = trimLeadingZeros(storedPIDs)
				reverseInts(storedPIDs)
			} else {
				storedPIDs = trimTrailingZeros(storedPIDs)
			}

			L := longestCommonSuffixLength(leafPIDs, storedPIDs)
			if L < sessionCommonSuffixThreshold {
				debugSession("discover: skip file=%q stored=%v L_suffix=%d (< threshold %d)", base, storedPIDs, L, sessionCommonSuffixThreshold)
				continue
			}
			debugSession("discover: contender file=%q stored=%v L_suffix=%d", base, storedPIDs, L)
			if L > bestL {
				bestL = L
				bestFile = path
			}
		}
	}

	// 2) Legacy: session dirs (session.yaml inside dir named tau-session-ts-p0-...-p5)
	legacyPattern := filepath.Join(sessionBaseDir(), sessionDirPrefix+"-session-*")
	dirMatches, err := filepath.Glob(legacyPattern)
	if err != nil {
		debugSession("discover: glob legacy dirs err=%v", err)
	} else {
		for _, dir := range dirMatches {
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			base := filepath.Base(dir)
			_, sName, ok := parseSessionDirBase(base)
			if !ok {
				debugSession("discover: skip legacy dir=%q (parse failed)", base)
				continue
			}
			// Legacy stored PIDs are root-first; reverse to leaf-first for suffix matching
			sNameTrim := trimLeadingZeros(sName)
			reverseInts(sNameTrim)
			L := longestCommonSuffixLength(leafPIDs, sNameTrim)
			if L < sessionCommonSuffixThreshold {
				debugSession("discover: skip legacy dir=%q S_name=%v L_suffix=%d (< threshold %d)", base, sNameTrim, L, sessionCommonSuffixThreshold)
				continue
			}
			legacySessionFile := filepath.Join(dir, sessionFileName+".yaml")
			if _, err := os.Stat(legacySessionFile); err != nil {
				continue
			}
			debugSession("discover: contender legacy dir=%q S_name=%v L_suffix=%d", base, sNameTrim, L)
			if L > bestL {
				bestL = L
				bestFile = legacySessionFile
			}
		}
	}

	if bestL >= sessionCommonSuffixThreshold && bestFile != "" {
		debugSession("discover: selected path=%q L=%d (>= threshold %d) => reusing", bestFile, bestL, sessionCommonSuffixThreshold)
		return bestFile, nil
	}
	debugSession("discover: no match (best L=%d, threshold=%d) => creating new session file", bestL, sessionCommonSuffixThreshold)

	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	fileBase := sessionDirBaseNameFromLeafPath(leafPIDs)
	if fileBase == "" {
		return "", fmt.Errorf("session: invalid path for file name")
	}
	sessionFilePath := filepath.Join(root, fileBase+".yaml")
	// Touch file so it exists; seer will open and use it
	f, err := os.OpenFile(sessionFilePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	f.Close()
	debugSession("discover: created file=%q (leaf PIDs len=%d)", sessionFilePath, len(leafPIDs))
	return sessionFilePath, nil
}

// ensureExactSessionDir: no-op for file-based sessions (one YAML per session, path in filename).
// For tests using LoadSessionInDir(dir) with a legacy dir name, may switch to exact dir.
// Call before any setKey/deleteKey.
func ensureExactSessionDir() error {
	if _session == nil {
		return nil
	}
	// File-based: we use a single session file; no switching.
	if _sessionDocName != "" && _sessionDocName != sessionFileName {
		return nil
	}
	base := filepath.Base(_sessionDir)
	if !strings.HasPrefix(base, sessionDirPrefix+"-session-") {
		return nil
	}
	// Legacy (tests): switch to exact dir for current PIDs if different.
	pids := currentSessionPidList()
	exact := sessionDirFromPidList(pids)
	if exact == "" {
		return fmt.Errorf("session: invalid pid list length")
	}
	if _sessionDir == exact {
		return nil
	}
	if err := os.MkdirAll(exact, 0700); err != nil {
		return err
	}
	sessionFile := sessionFileName + ".yaml"
	src := filepath.Join(_sessionDir, sessionFile)
	dst := filepath.Join(exact, sessionFile)
	if data, err := os.ReadFile(src); err == nil {
		if err := os.WriteFile(dst, data, 0600); err != nil {
			return err
		}
	}
	debugSession("ensureExactSessionDir: switched to exact dir=%q", exact)
	return LoadSessionInDir(exact)
}
