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

func debugSession(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "[tau session] "+format+"\n", args...)
}

const debugProcessTreeMaxDepth = 20

// debugProcessTree dumps the full process ancestry from getppid() to root (pid 1 or unknown).
// Goes up to debugProcessTreeMaxDepth levels so we have enough info to debug session discovery.
// Each line: depth pid ppid exe [runner][shell]
func debugProcessTree() {
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

// sessionDirBaseName returns the base name for a session dir: tau-session-<timestampMs>-pid[x-1]-...-pid[0].
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

// sessionDirFromPidList builds the session directory path for creation (uses current time in ms).
// Format: tau-session-<timestampMs>-pid[x-1]-...-pid[0].
func sessionDirFromPidList(pids []int) string {
	name := sessionDirBaseName(pids, time.Now().UnixMilli())
	if name == "" {
		return ""
	}
	return filepath.Join(sessionBaseDir(), name)
}

// parseSessionDirBase parses a session dir base name (e.g. "tau-session-1234567890-0-0-0-123-456") into timestamp and pids.
// Returns (0, nil, false) if format is invalid.
func parseSessionDirBase(name string) (timestampMs int64, pids []int, ok bool) {
	prefix := sessionDirPrefix + "-session-"
	if !strings.HasPrefix(name, prefix) {
		return 0, nil, false
	}
	rest := strings.TrimPrefix(name, prefix)
	parts := strings.Split(rest, "-")
	if len(parts) != sessionAncestorDepth+1 {
		return 0, nil, false
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, nil, false
	}
	out := make([]int, sessionAncestorDepth)
	for i := 0; i < sessionAncestorDepth; i++ {
		n, err := strconv.Atoi(parts[i+1])
		if err != nil {
			return 0, nil, false
		}
		out[i] = n
	}
	return ts, out, true
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
	pids := currentSessionPidList()
	if len(pids) != sessionAncestorDepth {
		return "", fmt.Errorf("session: invalid pid list length")
	}
	pattern := filepath.Join(sessionBaseDir(), sessionDirPrefix+"-session-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		debugSession("discoverOrCreateConfigFileLoc: glob err=%v", err)
		goto create
	}
	{
		var fullMatchDir string
		var fullMatchTs int64
		var suffixMatchDir string
		var suffixMatchCount int
		var suffixMatchTs int64
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				continue
			}
			base := filepath.Base(path)
			ts, other, ok := parseSessionDirBase(base)
			if !ok {
				continue
			}
			// Current pids are [closest,...,root]; parsed other is [root,...,closest]. Compare from tau-ward (right): pids[i] vs other[x-1-i].
			count := 0
			for i := 0; i < sessionAncestorDepth && pids[i] == other[sessionAncestorDepth-1-i]; i++ {
				count++
			}
			if count == sessionAncestorDepth {
				if ts > fullMatchTs {
					fullMatchTs = ts
					fullMatchDir = path
				}
			} else if count > suffixMatchCount || (count == suffixMatchCount && ts > suffixMatchTs) {
				suffixMatchCount = count
				suffixMatchTs = ts
				suffixMatchDir = path
			}
		}
		if fullMatchDir != "" {
			debugSession("discoverOrCreateConfigFileLoc: full match dir=%q", fullMatchDir)
			return fullMatchDir, nil
		}
		if suffixMatchDir != "" {
			debugSession("discoverOrCreateConfigFileLoc: longest suffix match dir=%q", suffixMatchDir)
			return suffixMatchDir, nil
		}
	}
create:
	dir := sessionDirFromPidList(pids)
	if dir == "" {
		return "", fmt.Errorf("session: invalid pid list length")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	debugSession("discoverOrCreateConfigFileLoc: created dir=%q", dir)
	return dir, nil
}

// ensureExactSessionDir switches the session to the exact path for the current process when we are about to mutate.
// If the session was loaded from a longest-suffix match, we create the exact path, copy the session file there, and reload.
// Skips switching when _sessionDir is not a discovered session dir (e.g. tests use LoadSessionInDir with a custom path).
// Call before any setKey/deleteKey.
func ensureExactSessionDir() error {
	if _session == nil {
		return nil
	}
	base := filepath.Base(_sessionDir)
	if !strings.HasPrefix(base, sessionDirPrefix+"-session-") {
		return nil // not a discovered session dir (e.g. test temp dir), do not switch
	}
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
