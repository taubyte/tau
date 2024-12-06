package engine

import (
	"fmt"
	"strings"

	pathUtils "github.com/taubyte/utils/path"
)

func keyFromPath(path []string) string {
	return "/" + pathUtils.Join(append(Prefix, path...))
}

func regExkeyFromPath(path []string) string {
	return "\\/" + strings.Join(append(Prefix, path...), "\\/")
}

func pathFromKey(key string) ([]string, error) {
	_path := pathUtils.Split(key)
	if len(_path) < len(Prefix) {
		return nil, fmt.Errorf("key `%s` is too short", key)
	}
	for _, p := range Prefix {
		if _path[0] != p {
			return nil, fmt.Errorf("key `%s` not absolute", key)
		}
		_path = _path[1:]
	}

	return _path, nil
}

func relativePathFromKey(path []string, key string) ([]string, error) {
	_path, err := pathFromKey(key)
	if err != nil {
		return nil, err
	}
	if len(_path) < len(path) {
		return nil, fmt.Errorf("key `%s` is too short", key)
	}
	return _path[len(path):], nil

}
