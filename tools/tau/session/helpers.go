package session

import (
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

func setKey(key string, value interface{}) (err error) {
	if err := ensureExactSessionDir(); err != nil {
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
	if err := ensureExactSessionDir(); err != nil {
		return err
	}
	err = _session.Document().Get(key).Delete().Commit()
	if err != nil {
		return singletonsI18n.SessionDeletingKeyFailed(key, err)
	}

	return _session.root.Sync()
}
