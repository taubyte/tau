package artifact

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestJoinWithinIsNotAStringPrefixTest(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "checkout")
	require.Equal(t, root, joinWithin(root, "../checkout-2/secrets"))
	require.Equal(t, root, joinWithin(root, "../checkoutx"))
}
