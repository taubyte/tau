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

// aWorkload is the resource doing the naming: its OWN id and kind are what a
// fall-through arm is asked about, and what the component arm must not use.
var aWorkload = Target{Project: "shop", Environment: "prod", Kind: "function", Name: "checkout"}

const (
	theComponent = "QmTheComponentsOwnResourceID"
	theWorkload  = "QmTheWorkloadsOwnResourceID"
)

func source(fields ...string) specs.Resource {
	block := map[string]any{}
	for i := 0; i+1 < len(fields); i += 2 {
		block[fields[i]] = fields[i+1]
	}
	return specs.Resource{"source": block}
}

func TestArtifactOf(t *testing.T) {
	type testCase struct {
		name     string
		resource specs.Resource
		seam     *asked

		artifact string
		err      error  // errors.Is
		says     string // substring of the refusal
		asks     bool   // the seam was asked exactly once
		askedFor string // ...about this id
		askedAs  string // ...under this kind
	}
	for _, tc := range []testCase{
		// A component's id is asked about, not answered with, and asked about as a
		// `component`, since the artifact is published under the component's own id.
		{
			name:     "a component reference is resolved through the seam",
			resource: source("kind", "component", "component", theComponent),
			seam:     &asked{answer: "bafyrealartifact"},
			artifact: "bafyrealartifact",
			asks:     true,
			askedFor: theComponent,
			askedAs:  "component",
		},
		// `resourceID` is the naming workload's; passing it here would resolve a different
		// build, or none.
		{
			name:     "and never by the naming workload's own id",
			resource: source("kind", "component", "component", theComponent),
			seam:     &asked{answer: "bafy"},
			artifact: "bafy",
			asks:     true,
			askedFor: theComponent, // NOT theWorkload
			askedAs:  "component",  // NOT the workload's kind
		},
		// A nameless reference is refused BY NAME rather than asked about as "".
		{
			name:     "a component reference with nothing in it is refused",
			resource: source("kind", "component"),
			seam:     &asked{answer: "bafy"},
			err:      ErrNoArtifact,
			says:     "function/checkout names no component",
		},
		// A node with no artifact seam cannot run a component, and the refusal names which.
		{
			name:     "a component with no seam wired is refused naming it",
			resource: source("kind", "component", "component", theComponent),
			err:      ErrNoArtifact,
			says:     theComponent,
		},
		// An unbuilt component must not resolve to "".
		{
			name:     "a failing seam refuses the component",
			resource: source("kind", "component", "component", theComponent),
			seam:     &asked{err: errUnbuilt},
			err:      errUnbuilt,
			asks:     true,
			askedFor: theComponent,
			askedAs:  "component",
		},

		// The other arms are unchanged.
		{
			name:     "an image is its own reference and asks nothing",
			resource: source("kind", "image", "image", "ghcr.io/acme/db:16"),
			seam:     &asked{answer: "never asked for"},
			artifact: "ghcr.io/acme/db:16",
		},
		{
			name:     "a builtin has no artifact and asks nothing",
			resource: source("kind", specs.SourceBlockSourceKindBuiltin, specs.SourceBlockSourceKindBuiltin, "step"),
			seam:     &asked{answer: "never asked for"},
			artifact: "", // shipped by the runtime; asking for one is the bug
		},
		{
			name:     "anything else is asked about by the workload's own id and kind",
			resource: source("kind", "git", "git", "https://github.com/acme/app"),
			seam:     &asked{answer: "bafybuilt"},
			artifact: "bafybuilt",
			asks:     true,
			askedFor: theWorkload,
			askedAs:  "function",
		},
		{
			name:     "a bare path in this repository is the same fall-through",
			resource: specs.Resource{"source": "services/checkout"},
			seam:     &asked{answer: "bafybuilt"},
			artifact: "bafybuilt",
			asks:     true,
			askedFor: theWorkload,
			askedAs:  "function",
		},
		{
			name:     "and with no seam wired it is refused naming the resource",
			resource: specs.Resource{"source": "services/checkout"},
			err:      ErrNoArtifact,
			says:     "function/checkout",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A nil *asked is a nil interface, which is the node with no seam.
			var seam Artifacts
			if tc.seam != nil {
				seam = tc.seam
			}

			got, err := resolving(seam).artifactOf(t.Context(), aWorkload, tc.resource, theWorkload)

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
				require.Zero(t, tc.seam.calls, "the seam was asked and should not have been")
				return
			}
			require.Equal(t, 1, tc.seam.calls, "the seam was asked exactly once")
			require.Equal(t, tc.askedFor, tc.seam.id, "asked about the wrong resource")
			require.Equal(t, tc.askedAs, tc.seam.kind, "asked under the wrong kind")
			require.Equal(t, aWorkload.Project, tc.seam.project)
			require.Equal(t, aWorkload.Environment, tc.seam.environment)
		})
	}
}

var errUnbuilt = errors.New("the build service has no artifact for that resource")

func TestTwoWorkloadsNamingOneComponentRunTheSameBuild(t *testing.T) {
	seam := &asked{answer: "bafyoneartifact"}
	s := resolving(seam)
	shared := source("kind", "component", "component", theComponent)

	first, err := s.artifactOf(t.Context(), aWorkload, shared, "QmFirstWorkload")
	require.NoError(t, err)
	firstAsked := seam.id

	other := aWorkload
	other.Name, other.Kind = "refunds", "worker"
	second, err := s.artifactOf(t.Context(), other, shared, "QmSecondWorkload")
	require.NoError(t, err)

	require.Equal(t, first, second, "one component, one build")
	require.Equal(t, theComponent, firstAsked)
	require.Equal(t, theComponent, seam.id,
		"the second asked about the component too, not about itself")
}
