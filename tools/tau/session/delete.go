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
	// Discover session file using leaf-side suffix matching (same logic as discoverOrCreateConfigFileLoc)
	leafPIDs := ancestorPIDs(maxAncestorDepthForPath)
	root := sessionRootDir()
	pattern := filepath.Join(root, sessionDirPrefix+"-session-*.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return singletonsI18n.SessionNotFound()
	}
	var bestFile string
	var bestL int
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		baseNoExt := strings.TrimSuffix(filepath.Base(path), ".yaml")
		storedPIDs, ok := parseSessionFileBase(baseNoExt)
		if !ok {
			_, storedPIDs, ok = parseSessionDirBase(baseNoExt)
			if !ok {
				continue
			}
			// Legacy stored PIDs are root-first; reverse to leaf-first for suffix matching
			storedPIDs = trimLeadingZeros(storedPIDs)
			reverseInts(storedPIDs)
		} else {
			storedPIDs = trimTrailingZeros(storedPIDs)
		}
		L := longestCommonSuffixLength(leafPIDs, storedPIDs)
		if L > bestL {
			bestL = L
			bestFile = path
		}
	}
	// Apply the same threshold as discovery — don't delete a weakly-matching session
	if bestFile == "" || bestL < sessionCommonSuffixThreshold {
		return singletonsI18n.SessionNotFound()
	}
	if err := os.Remove(bestFile); err != nil && !os.IsNotExist(err) {
		return singletonsI18n.SessionDeleteFailed(bestFile, err)
	}
	return nil
}
