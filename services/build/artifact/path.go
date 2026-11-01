package artifact

import (
	"path/filepath"
	"strings"
)

// joinWithin joins a config-supplied path under root and never leaves it. The
// path comes from a repository anyone with write access controls, and an escape
// would bind-mount what it reached into their build. An escape collapses to the
// root; the build then fails on its missing output.
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
