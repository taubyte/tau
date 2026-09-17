package protogen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/tools/protogen"
)

// TestCIPinsTheSameBufThisRepoDoes — two pins, one version, and nothing else
// makes them agree.
//
// The rules in pkg/proto are asked about whatever buf builds, so CI installing
// one version while every developer's fallback fetches another means the guard
// passes in one place and fails in the other, over a schema neither of them got
// wrong. The workflow's `go install` line is where the version used to live
// alone; protogen.BufVersion is where it lives now, and this is what stops the
// old copy drifting.
func TestCIPinsTheSameBufThisRepoDoes(t *testing.T) {
	root, err := protogen.FindRepoRoot(mustWd(t))
	require.NoError(t, err)

	body, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "pre-commit.yml"))
	require.NoError(t, err)

	const prefix = "github.com/bufbuild/buf/cmd/buf@"
	var found []string
	for _, line := range strings.Split(string(body), "\n") {
		at := strings.Index(line, prefix)
		if at < 0 {
			continue
		}
		found = append(found, strings.TrimSpace(line[at+len(prefix):]))
	}

	require.NotEmpty(t, found, "the workflow no longer installs buf; if that is deliberate, this test is what should say so")
	for _, version := range found {
		require.Equal(t, protogen.BufVersion, version,
			"CI installs a buf this repository does not pin")
	}
}

func mustWd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}
