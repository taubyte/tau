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
// Goes up to debugProcessTreeMaxDepth levels so we have enough info to debug session discovery.
// Each line: depth pid ppid exe [runner][shell]
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

// isRunnerOrShell returns true if the executable is a known runner we should climb past (go, node)
// or a shell we want to key session by (bash, sh, cmd, etc.).
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

// processName returns the executable name for pid (e.g. "bash.exe", "mintty.exe").
// Uses go-ps when available; on Windows falls back to native API when go-ps cannot see the process.
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
// p0 = getppid(), p1 = parent(p0), etc. Stops at maxDepth or when parent is 0/1 or unavailable.
// On Windows, when go-ps cannot see a process, getParentPIDNative is used to keep climbing.
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
// Uses ancestorPIDs then reverses so P[0] is root, P[len-1] is closest to tau.
func ancestorPathFromRoot(maxDepth int) []int {
	pids := ancestorPIDs(maxDepth)
	for i, j := 0, len(pids)-1; i < j; i, j = i+1, j-1 {
		pids[i], pids[j] = pids[j], pids[i]
	}
	return pids
}

// longestCommonPrefixLength returns the length of the longest common prefix of a and b (element-wise from index 0).
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

// sessionDirBaseNameFromRootPath builds a session file base name from a root-first path P (first 6 PIDs, padded with 0).
// No timestamp so the same process tree always maps to the same file and it can be updated (mutable).
func sessionDirBaseNameFromRootPath(rootFirstPath []int, _ int64) string {
	six := make([]int, sessionAncestorDepth)
	for i := 0; i < sessionAncestorDepth; i++ {
		if i < len(rootFirstPath) {
			six[i] = rootFirstPath[i]
		} else {
			six[i] = 0
		}
	}
	parts := make([]string, sessionAncestorDepth)
	for i := range six {
		parts[i] = strconv.Itoa(six[i])
	}
	return sessionDirPrefix + "-session-" + strings.Join(parts, "-")
}

// sessionDirBaseName returns the base name for a session dir/file: tau-session-<timestampMs>-pid[x-1]-...-pid[0].
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

// sessionRootDir returns the single directory that contains session YAML files (path in filename).
func sessionRootDir() string {
	if sessionRootDirOverride != "" {
		return sessionRootDirOverride
	}
	return filepath.Join(sessionBaseDir(), "tau")
}

// sessionDirFromPidList builds the session directory path for creation (uses current time in ms).
// Format: tau-session-<timestampMs>-pid[x-1]-...-pid[0].
func sessionDirFromPidList(pids []int) string {
	name := sessionDirBaseName(pids, time.Now().UnixMilli())
	if name == "" {
		return ""
	}
	return filepath.Join(sessionBaseDir(), name)
}

// parseSessionDirBase parses a session file/dir base name into timestamp and pids (root-first).
// Accepts: "tau-session-<ts>-<p0>-...-<p5>" (7 parts) or "tau-session-<p0>-...-<p5>" (6 parts, no timestamp).
// Returns (0, nil, false) if format is invalid.
func parseSessionDirBase(name string) (timestampMs int64, pids []int, ok bool) {
	prefix := sessionDirPrefix + "-session-"
	if !strings.HasPrefix(name, prefix) {
		return 0, nil, false
	}
	rest := strings.TrimPrefix(name, prefix)
	parts := strings.Split(rest, "-")
	var start int
	if len(parts) == sessionAncestorDepth+1 {
		ts, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, nil, false
		}
		timestampMs = ts
		start = 1
	} else if len(parts) != sessionAncestorDepth {
		return 0, nil, false
	}
	out := make([]int, sessionAncestorDepth)
	for i := 0; i < sessionAncestorDepth; i++ {
		n, err := strconv.Atoi(parts[start+i])
		if err != nil {
			return 0, nil, false
		}
		out[i] = n
	}
	return timestampMs, out, true
}

// currentSessionPidList returns the current process ancestor list padded to sessionAncestorDepth (pad right with 0).
func currentSessionPidList() []int {
	pids := ancestorPIDs(sessionAncestorDepth)
	for len(pids) < sessionAncestorDepth {
		pids = append(pids, 0)
	}
	return pids
}

func discoverOrCreateConfigFileLoc() (string, error) {
	debugProcessTree()
	P := ancestorPathFromRoot(maxAncestorDepthForPath)
	debugSession("discover: current path P (root-first) len=%d P=%v threshold=%d", len(P), P, sessionCommonRootThreshold)

	var bestFile string
	var bestL int
	var bestTs int64

	// 1) Session YAML files in session root (path in filename)
	root := sessionRootDir()
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
			ts, sName, ok := parseSessionDirBase(baseNoExt)
			if !ok {
				debugSession("discover: skip file=%q (parse failed)", base)
				continue
			}
			sNameTrim := trimLeadingZeros(sName)
			Lname := longestCommonPrefixLength(P, sNameTrim)
			if Lname < sessionCommonRootThreshold {
				debugSession("discover: skip file=%q S_name=%v L_name=%d (< threshold %d)", base, sNameTrim, Lname, sessionCommonRootThreshold)
				continue
			}
			L := Lname
			debugSession("discover: contender file=%q S_name=%v L=%d ts=%d", base, sNameTrim, L, ts)
			if L > bestL || (L == bestL && ts > bestTs) {
				bestL = L
				bestTs = ts
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
			ts, sName, ok := parseSessionDirBase(base)
			if !ok {
				debugSession("discover: skip legacy dir=%q (parse failed)", base)
				continue
			}
			sNameTrim := trimLeadingZeros(sName)
			Lname := longestCommonPrefixLength(P, sNameTrim)
			if Lname < sessionCommonRootThreshold {
				debugSession("discover: skip legacy dir=%q S_name=%v L_name=%d (< threshold %d)", base, sNameTrim, Lname, sessionCommonRootThreshold)
				continue
			}
			legacySessionFile := filepath.Join(dir, sessionFileName+".yaml")
			if _, err := os.Stat(legacySessionFile); err != nil {
				continue
			}
			L := Lname
			debugSession("discover: contender legacy dir=%q S_name=%v L=%d ts=%d", base, sNameTrim, L, ts)
			if L > bestL || (L == bestL && ts > bestTs) {
				bestL = L
				bestTs = ts
				bestFile = legacySessionFile
			}
		}
	}

	if bestL >= sessionCommonRootThreshold && bestFile != "" {
		debugSession("discover: selected path=%q L=%d (>= threshold %d) => reusing", bestFile, bestL, sessionCommonRootThreshold)
		return bestFile, nil
	}
	debugSession("discover: no match (best L=%d, threshold=%d) => creating new session file", bestL, sessionCommonRootThreshold)

	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	fileBase := sessionDirBaseNameFromRootPath(P, time.Now().UnixMilli())
	if fileBase == "" {
		return "", fmt.Errorf("session: invalid path for file name")
	}
	sessionFilePath := filepath.Join(root, fileBase+".yaml")
	// Touch file so it exists; seer will open and use it
	f, err := os.OpenFile(sessionFilePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	debugSession("discover: created file=%q (path len=%d)", sessionFilePath, len(P))
	return sessionFilePath, nil
}

// ensureExactSessionDir: no-op for file-based sessions (one YAML per session, path in filename). For tests using LoadSessionInDir(dir) with a legacy dir name, may switch to exact dir.
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
	// Legacy (tests): switch to exact dir for current 6 PIDs if different.
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
