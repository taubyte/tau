//go:build dreaming

package dream_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// refused_test holds the projects under projects/refused: each is something an
// author can write, and the vocabulary claims to refuse. A claim nobody asks the
// vocabulary to keep is one nothing notices it stopped keeping.
//
// THE REFUSAL IS THE AUDIT'S, and that is a fact to state rather than hide: these
// compile. A container with no image and no build is a statement the config
// itself can answer, so it is checkBuild's (pkg/tcc/taubyte/v2/schema/checks.go)
// to call broken — and today only the editor runs that audit. Nothing on the
// deploy path does, so a project like this one publishes and fails when its
// container is asked to start.
//
// IT IS PAIRED WITH THE PROJECT THAT WORKS. The refused one is also unrouted,
// which the audit says too; that is not what it is about. Neither writes any
// `regions`, and both are placed — in the one region a build that serves one
// places everything in — so being unplaced is not a reason either is broken. The
// same audit over projects/with/application finds nothing broken at all, so the
// finding asserted here is the one thing the two differ in.
func TestAnApplicationWithNothingToRunIsBroken(t *testing.T) {
	broken := brokenIn(t, "refused/application/noartifact")
	require.Contains(t, broken, "application: names no image and declares no build")
	require.NotContains(t, broken, "application: is not placed anywhere")

	require.Empty(t, brokenIn(t, "with/application"),
		"the project that runs has nothing broken, so the refusal above is about the image")
}

// brokenIn is every Broken finding the audit makes about one corpus project.
func brokenIn(t *testing.T, name string) []string {
	t.Helper()
	c, err := schema.New(schema.WithLocal(filepath.Join("projects", name)), schema.WithBranch("main"))
	require.NoError(t, err)
	obj, _, err := c.Compile(context.Background())
	require.NoError(t, err, "%s compiles; any refusal is the audit's", name)

	var out []string
	for _, f := range schema.Audit(obj.Flat(), schema.StubResolver()) {
		if f.Severity == schema.Broken {
			out = append(out, f.Kind+": "+f.Title)
		}
	}
	return out
}
