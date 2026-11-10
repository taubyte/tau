package path

import (
	pathUtils "path"
	"strings"
)

/*
Split splits the string by `/` separator, then cleans up any empty strings

example:

/home/taubyte with strings.Split would evaluate to []string{"", "home", "taubyte"}

Split will evaluate `/home/taubyte` as []string{"home", "taubyte"}
*/
func Split(path string) []string {
	names := make([]string, 0)
	// The old loop walked up to "/" and never ended for a relative path.
	for _, name := range strings.Split(pathUtils.Clean(path), "/") {
		if name != "" && name != "." {
			names = append(names, name)
		}
	}
	return names
}
