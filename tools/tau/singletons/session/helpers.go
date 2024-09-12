package session

import (
	singletonsI18n "github.com/taubyte/tau/tools/tau/i18n/singletons"
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
	err = _session.Document().Get(key).Set(value).Commit()
	if err != nil {
		return singletonsI18n.SessionSettingKeyFailed(key, value, err)
	}

	return _session.root.Sync()
}

func deleteKey(key string) (err error) {
	err = _session.Document().Get(key).Delete().Commit()
	if err != nil {
		return singletonsI18n.SessionDeletingKeyFailed(key, err)
	}

	return _session.root.Sync()
}
