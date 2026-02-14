package session

import (
	"os"
	"path/filepath"

	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/shared"
	"golang.org/x/exp/slices"
)

func getKey[T any](key string) (value T, exist bool) {
	keys, err := _session.keys()
	if err != nil {
		return
	}

	if !slices.Contains(keys, key) {
		return
	}

	err = _session.Document().Get(key).Value(&value)
	if err == nil {
		return value, true
	}

	return
}

// forkSession copies the current session file to a new file named with current PIDs and reloads.
// No-op if already using the file for current PIDs, or when in test mode (LoadSessionInDir).
func forkSession() error {
	if _session == nil {
		return nil
	}
	if _sessionDocName == sessionFileName {
		debugSession("forkSession: no-op (test mode)")
		return nil
	}
	currentPIDs := ancestorPIDs(sessionMaxAncestors)
	newDocName := sessionFileBaseName(currentPIDs)
	if newDocName == "" {
		return nil
	}
	newPath := filepath.Join(_sessionDir, newDocName+".yaml")
	currentPath := filepath.Join(_sessionDir, _sessionDocName+".yaml")
	if newPath == currentPath {
		debugSession("forkSession: no-op (same file doc=%q)", _sessionDocName)
		return nil
	}
	data, err := os.ReadFile(currentPath)
	if err != nil {
		return singletonsI18n.SessionCreateFailed(newPath, err)
	}
	if err := os.WriteFile(newPath, data, 0600); err != nil {
		return singletonsI18n.SessionCreateFailed(newPath, err)
	}
	debugSession("forkSession: copied %q -> %q", currentPath, newPath)
	return LoadSessionAt(newPath)
}

func setKey(key string, value any) (err error) {
	if err := forkSession(); err != nil {
		return err
	}
	debugSession("setKey: key=%q value=%v", key, value)
	err = _session.Document().Get(key).Set(value).Commit()
	if err != nil {
		debugSession("setKey: Commit err=%v", err)
		return singletonsI18n.SessionSettingKeyFailed(key, value, err)
	}
	err = _session.root.Sync()
	debugSession("setKey: Sync err=%v", err)
	if err != nil {
		return err
	}
	return nil
}

func deleteKey(key string) (err error) {
	if err := forkSession(); err != nil {
		return err
	}
	debugSession("deleteKey: key=%q", key)
	err = _session.Document().Get(key).Delete().Commit()
	if err != nil {
		return singletonsI18n.SessionDeletingKeyFailed(key, err)
	}
	err = _session.root.Sync()
	debugSession("deleteKey: Sync err=%v", err)
	return err
}
