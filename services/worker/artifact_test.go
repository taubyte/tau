package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// What is asserted is what the seam was asked, never what the fake answered: the
// harness's artifactFunc concocts a registry reference, so the arguments (which id,
// under which kind, asked at all) are the behaviour.

type asked struct {
	project     string
	environment string
	kind        string
	id          string

	answer string
	err    error
	calls  int
}

func (a *asked) Artifact(_ context.Context, project, environment, kind, id string) (string, error) {
	a.calls++
	a.project, a.environment, a.kind, a.id = project, environment, kind, id
	if a.err != nil {
		return "", a.err
	}
	return a.answer, nil
}

// resolving is a Service with nothing but the seam, which is all artifactOf
// reads. A nil seam is a node that resolves no artifacts.
func resolving(seam Artifacts) *Service {
	s := &Service{}
	if seam != nil {
		s.artifacts = seam
	}
	return s
}

// aWorkload is the resource naming the package. Its own id and kind are never what
// the seam is asked about: what it runs is published under the package's.
var aWorkload = Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}

const thePackage = "QmThePackagesOwnResourceID"

func runs(kind, field, value string) ranPackage {
	src := specs.PackageSource{Kind: kind}
	switch field {
	case "image":
		src.Image = value
	case "builtin":
		src.Builtin = value
	case "git":
		src.Git = value
	}
	return ranPackage{Package: specs.Package{Name: "paylib", Source: src}, ID: thePackage}
}

func TestArtifactOf(t *testing.T) {
	type testCase struct {
		name string
		pkg  ranPackage
		seam *asked

		artifact string
		err      error  // errors.Is
		says     string // substring of the refusal
		asks     bool   // the seam was asked exactly once, about the package
	}
	for _, tc := range []testCase{
		{
			name:     "a built package is asked about by its own id, as a package",
			pkg:      runs("git", "git", "https://github.com/acme/paylib"),
			seam:     &asked{answer: "bafybuilt"},
			artifact: "bafybuilt",
			asks:     true,
		},
		{
			name:     "and so is one built from this repository",
			pkg:      runs("", "", ""),
			seam:     &asked{answer: "bafybuilt"},
			artifact: "bafybuilt",
			asks:     true,
		},
		{
			name:     "an image is its own reference and asks nothing",
			pkg:      runs(specs.PackageSourceKindImage, "image", "ghcr.io/acme/db:16"),
			seam:     &asked{answer: "never asked for"},
			artifact: "ghcr.io/acme/db:16",
		},
		{
			name:     "a builtin has no artifact and asks nothing",
			pkg:      runs(specs.PackageSourceKindBuiltin, "builtin", "step"),
			seam:     &asked{answer: "never asked for"},
			artifact: "", // shipped by the runtime; asking for one is the bug
		},
		{
			name: "with no seam wired a built package is refused naming it",
			pkg:  runs("git", "git", "https://github.com/acme/paylib"),
			err:  ErrNoArtifact,
			says: "package paylib",
		},
		{
			name: "an unbuilt package must not resolve to nothing",
			pkg:  runs("git", "git", "https://github.com/acme/paylib"),
			seam: &asked{err: errUnbuilt},
			err:  errUnbuilt,
			asks: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil *asked is a nil interface, which is the node with no seam.
			var seam Artifacts
			if tc.seam != nil {
				seam = tc.seam
			}

			got, err := resolving(seam).artifactOf(t.Context(), aWorkload, tc.pkg)

			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				if tc.says != "" {
					require.ErrorContains(t, err, tc.says)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.artifact, got)
			}

			if tc.seam == nil {
				return
			}
			if !tc.asks {
				require.Zero(t, tc.seam.calls, "the seam was asked about a resource that names its own artifact")
				return
			}
			require.Equal(t, 1, tc.seam.calls)
			require.Equal(t, thePackage, tc.seam.id)
			require.Equal(t, specs.KindPackage, tc.seam.kind)
			require.Equal(t, aWorkload.Project, tc.seam.project)
			require.Equal(t, aWorkload.Environment, tc.seam.environment)
		})
	}
}

var errUnbuilt = errors.New("not built yet")

func TestTwoWorkloadsNamingOnePackageRunTheSameBuild(t *testing.T) {
	seam := &asked{answer: "bafyoneartifact"}
	s := resolving(seam)
	shared := runs("git", "git", "https://github.com/acme/paylib")

	first, err := s.artifactOf(t.Context(), aWorkload, shared)
	require.NoError(t, err)

	other := aWorkload
	other.Name, other.Kind = "refunds", "worker"
	second, err := s.artifactOf(t.Context(), other, shared)
	require.NoError(t, err)

	require.Equal(t, first, second, "one package, one build")
	require.Equal(t, thePackage, seam.id, "the second asked about the package too, not about itself")
}
