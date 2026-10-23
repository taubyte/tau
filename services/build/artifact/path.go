package artifact

import (
	"path/filepath"
	"strings"
)

// joinWithin puts a config-supplied path under a directory this process owns,
// and refuses to leave it.
//
// THE PATH IS A STRANGER'S. It comes out of a repository's own configuration,
// which anybody with write access to that repository controls, and it is joined
// against a directory holding somebody else's checkout. A `..` that escaped
// would bind-mount whatever it reached into a container running that same
// stranger's build commands.
//
// A path that escapes collapses to the root rather than erroring, because the
// caller has nothing better to do with it: the build then runs in the checkout
// root, produces nothing at the declared output, and fails with a message about
// the output — which is the truth.
func joinWithin(root, path string) string {
	if path == "" {
		return root
	}

	cleaned := filepath.Clean(filepath.Join(root, path))
	rooted := filepath.Clean(root)
	if cleaned != rooted && !strings.HasPrefix(cleaned, rooted+string(filepath.Separator)) {
		return rooted
	}
	return cleaned
}

// within is a config-supplied path cleaned to one inside the checkout, and ""
// for the checkout itself or one that would leave it — joinWithin's rule, for a
// path that is carried rather than joined.
func within(path string) string {
	root := string(filepath.Separator) + "checkout"
	rel, err := filepath.Rel(root, joinWithin(root, path))
	if err != nil || rel == "." {
		return ""
	}
	return rel
}
