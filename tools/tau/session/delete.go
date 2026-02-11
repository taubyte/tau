package session

import (
	"os"
	"path/filepath"
	"strings"

	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
)

func Delete() error {
	if _session != nil && _sessionDir != "" {
		if _sessionDocName != "" && _sessionDocName != sessionFileName {
			// File-based: remove the single session file
			filePath := filepath.Join(_sessionDir, _sessionDocName+".yaml")
			if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
				return singletonsI18n.SessionDeleteFailed(filePath, err)
			}
			return nil
		}
		if strings.HasPrefix(filepath.Base(_sessionDir), sessionDirPrefix+"-session-") {
			if err := os.RemoveAll(_sessionDir); err != nil {
				return singletonsI18n.SessionDeleteFailed(_sessionDir, err)
			}
			return nil
		}
	}
	// Discover session file (same logic as discoverOrCreateConfigFileLoc)
	P := ancestorPathFromRoot(maxAncestorDepthForPath)
	root := sessionRootDir()
	pattern := filepath.Join(root, sessionDirPrefix+"-session-*.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return singletonsI18n.SessionNotFound()
	}
	var bestFile string
	var bestL int
	var bestTs int64
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		baseNoExt := strings.TrimSuffix(filepath.Base(path), ".yaml")
		ts, sName, ok := parseSessionDirBase(baseNoExt)
		if !ok {
			continue
		}
		S := trimLeadingZeros(sName)
		L := longestCommonPrefixLength(P, S)
		if L > bestL || (L == bestL && ts > bestTs) {
			bestL = L
			bestTs = ts
			bestFile = path
		}
	}
	if bestFile == "" {
		return singletonsI18n.SessionNotFound()
	}
	if err := os.Remove(bestFile); err != nil && !os.IsNotExist(err) {
		return singletonsI18n.SessionDeleteFailed(bestFile, err)
	}
	return nil
}
