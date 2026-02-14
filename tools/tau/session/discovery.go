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

func debugSession(format string, args ...any) {
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
		debugSession("  [%2d] pid=%d ppid=%d exe=%q%s", depth, pid, ppid, exe, tags)
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

// pidSetIntersection returns the count of PIDs that appear in both a and b (set intersection).
func pidSetIntersection(a, b []int) int {
	set := make(map[int]bool)
	for _, p := range a {
		if p > 0 {
			set[p] = true
		}
	}
	count := 0
	for _, p := range b {
		if p > 0 && set[p] {
			count++
		}
	}
	return count
}

// sessionFileBaseName returns the session file base name (without .yaml) for the given leaf-first PIDs.
// Format: tau-session-<p0>-<p1>-...-<pN> (variable length, no zero-padding).
func sessionFileBaseName(pids []int) string {
	if len(pids) == 0 {
		return ""
	}
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	return sessionDirPrefix + "-session-" + strings.Join(parts, "-")
}

// parseSessionPIDs parses a session file/dir base name (new variable-length format).
// Format: "tau-session-<p0>-<p1>-...-<pN>" (all numeric parts).
// Returns (pids, true) or (nil, false) if invalid.
func parseSessionPIDs(name string) ([]int, bool) {
	prefix := sessionDirPrefix + "-session-"
	if !strings.HasPrefix(name, prefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(name, prefix)
	parts := strings.Split(rest, "-")
	if len(parts) == 0 {
		return nil, false
	}
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// parseLegacySessionPIDs parses legacy session file names into PIDs for set intersection.
// Handles: "tau-session-<p0>-...-<p15>" (old fixed-length file format).
// Returns (pids, true) or (nil, false) if invalid.
func parseLegacySessionPIDs(name string) ([]int, bool) {
	prefix := sessionDirPrefix + "-session-"
	if !strings.HasPrefix(name, prefix) {
		return nil, false
	}
	rest := strings.TrimPrefix(name, prefix)
	parts := strings.Split(rest, "-")
	// Old fixed-length format: exactly 16 parts
	if len(parts) == 16 {
		out := make([]int, 0, 16)
		for _, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	}
	return nil, false
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

func discoverOrCreateConfigFileLoc() (string, error) {
	debugProcessTree()
	leafPIDs := ancestorPIDs(sessionMaxAncestors)
	debugSession("discover: leaf-first PIDs len=%d P=%v", len(leafPIDs), leafPIDs)

	root := sessionRootDir()
	debugSession("discover: session root=%q", root)

	var bestFile string
	var bestIntersection int
	var bestModTime time.Time

	// 1) Session YAML files in root (tau-session-*.yaml)
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
			storedPIDs, ok := parseSessionPIDs(baseNoExt)
			if !ok {
				storedPIDs, ok = parseLegacySessionPIDs(baseNoExt)
				if !ok {
					debugSession("discover: skip file=%q (parse failed)", base)
					continue
				}
			}
			inter := pidSetIntersection(leafPIDs, storedPIDs)
			if inter < 1 {
				debugSession("discover: skip file=%q stored=%v intersection=%d (< 1)", base, storedPIDs, inter)
				continue
			}
			debugSession("discover: contender file=%q stored=%v intersection=%d", base, storedPIDs, inter)
			if inter > bestIntersection || (inter == bestIntersection && (bestFile == "" || info.ModTime().After(bestModTime))) {
				bestIntersection = inter
				bestFile = path
				bestModTime = info.ModTime()
			}
		}
	}

	if bestIntersection >= 1 && bestFile != "" {
		debugSession("discover: selected path=%q intersection=%d => reusing", bestFile, bestIntersection)
		return bestFile, nil
	}
	debugSession("discover: no match (best intersection=%d) => creating new session file", bestIntersection)

	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	fileBase := sessionFileBaseName(leafPIDs)
	if fileBase == "" {
		return "", fmt.Errorf("session: invalid path for file name")
	}
	sessionFilePath := filepath.Join(root, fileBase+".yaml")
	f, err := os.OpenFile(sessionFilePath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	f.Close()
	debugSession("discover: created file=%q (leaf PIDs len=%d)", sessionFilePath, len(leafPIDs))
	return sessionFilePath, nil
}
