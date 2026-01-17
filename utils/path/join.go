package path

import (
	"path"
)

// Join joins the string slice to a string separated by a `/`
func Join(names []string) string {
	return path.Join(names...)
}
