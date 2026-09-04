package artifact

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// joinWithin is the SECOND line. The compiler refuses a `..` in a source path
// where the config is read (IsRepoLocation), so a compiled object cannot carry
// one — but this joins a config-supplied string against a directory holding a
// stranger's checkout, and what it produces is bind-mounted into a container
// running that same stranger's build commands. It does not get to assume the
// only reader of a compiled object is the compiler that made it.
func TestJoinWithinNeverLeaves(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "checkout")

	for path, want := range map[string]string{
		"":                   root,
		".":                  root,
		"services/reserve":   filepath.Join(root, "services", "reserve"),
		"./services/reserve": filepath.Join(root, "services", "reserve"),
		"services//reserve":  filepath.Join(root, "services", "reserve"),
		"services/./reserve": filepath.Join(root, "services", "reserve"),
		"services/../apps":   filepath.Join(root, "apps"),
		"..":                 root,
		"../..":              root,
		"../../../../etc":    root,
		"services/../../etc": root,
		"/etc/shadow":        filepath.Join(root, "etc", "shadow"),
		"/":                  root,
	} {
		require.Equal(t, want, joinWithin(root, path), path)
	}
}

// A SIBLING WHOSE NAME STARTS THE SAME WAY IS STILL OUTSIDE. "/tmp/checkout-2"
// is not under "/tmp/checkout", and a prefix test without the separator would
// say it was.
func TestJoinWithinIsNotAStringPrefixTest(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "checkout")
	require.Equal(t, root, joinWithin(root, "../checkout-2/secrets"))
	require.Equal(t, root, joinWithin(root, "../checkoutx"))
}
