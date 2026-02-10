package session

import (
	"os"
	"path/filepath"
	"strings"

	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
)

func Delete() error {
	if _session != nil && _sessionDir != "" && strings.HasPrefix(filepath.Base(_sessionDir), sessionDirPrefix+"-session-") {
		if err := os.RemoveAll(_sessionDir); err != nil {
			return singletonsI18n.SessionDeleteFailed(_sessionDir, err)
		}
		return nil
	}
	pids := currentSessionPidList()
	pattern := filepath.Join(os.TempDir(), sessionDirPrefix+"-session-*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return singletonsI18n.SessionNotFound()
	}
	var bestDir string
	var bestTs int64
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		ts, other, ok := parseSessionDirBase(filepath.Base(path))
		if !ok {
			continue
		}
		match := true
		for i := range pids {
			if pids[i] != other[i] {
				match = false
				break
			}
		}
		if match && ts > bestTs {
			bestTs = ts
			bestDir = path
		}
	}
	if bestDir == "" {
		return singletonsI18n.SessionNotFound()
	}
	if err := os.RemoveAll(bestDir); err != nil {
		return singletonsI18n.SessionDeleteFailed(bestDir, err)
	}
	return nil
}
