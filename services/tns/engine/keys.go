package engine

import (
	"fmt"
	"regexp"
	"strings"

	pathUtils "github.com/taubyte/tau/utils/path"
)

func keyFromPath(path []string) string {
	return "/" + pathUtils.Join(append(Prefix, path...))
}

// regExkeyFromPath builds the pattern a RegEx lookup matches keys against.
//
// THE CALLER'S SEGMENTS ARE PATTERNS AND ARE NOT ESCAPED. That is what RegEx on
// the query means, and the one caller that sets it -- taucorder's regex lookup
// -- sends fragments on purpose. Escaping them here would leave the flag with
// nothing to do. The PREFIX is different: it is this package's own constant and
// names literal path segments, so it is quoted. That is a no-op for "tns"/"v1"
// and stays correct if the prefix ever holds a character a regex reads.
//
// IT IS ANCHORED, which it did not used to be. The filter matches with
// MatchString, which is unanchored, so the pattern for a lookup was satisfied by
// a key that merely CONTAINED it -- a path that happened to carry the prefix
// text further along answered a lookup that never asked about it. A lookup is
// rooted at the store's prefix by definition, so it says so.
func regExkeyFromPath(path []string) string {
	quoted := make([]string, 0, len(Prefix)+len(path))
	for _, p := range Prefix {
		quoted = append(quoted, regexp.QuoteMeta(p))
	}
	quoted = append(quoted, path...)

	return "^\\/" + strings.Join(quoted, "\\/")
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
