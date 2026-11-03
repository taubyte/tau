package zone_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/instance/zone"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// The adapter over maps and counters, including the index answering for an apex whose
// project will not load and a resolver on a node with no tier.

// index is a Hosts double: a name to a deployment, and a record of the walk.
type index struct {
	at    map[string][2]string
	asked []string
}

func (i *index) ResolveHost(_ context.Context, fqdn string) (string, string, error) {
	i.asked = append(i.asked, fqdn)
	if d, ok := i.at[fqdn]; ok {
		return d[0], d[1], nil
	}
	return "", "", errors.New("no such host")
}

// held is a Projects double.
type held struct {
	projects map[string]*specs.Project
	err      error
	spec     invoke.Spec
	specErr  error
	targets  []worker.Target
}

func (h *held) At(_ context.Context, at register.Deployment) (*specs.Project, register.Pointer, error) {
	if h.err != nil {
		return nil, register.Pointer{}, h.err
	}
	p, ok := h.projects[at.Project+"/"+at.Environment]
	if !ok {
		return nil, register.Pointer{}, errors.New("not deployed here")
	}
	return p, register.Pointer{}, nil
}

func (h *held) Resolve(_ context.Context, t worker.Target) (invoke.Spec, error) {
	h.targets = append(h.targets, t)
	return h.spec, h.specErr
}

// guest is a Guests double.
type guest struct {
	answer *invoke.Resolution
	err    error
	specs  []invoke.Spec
	asked  []invoke.Question
}

func (g *guest) Resolve(_ context.Context, spec invoke.Spec, q invoke.Question) (*invoke.Resolution, error) {
	g.specs = append(g.specs, spec)
	g.asked = append(g.asked, q)
	return g.answer, g.err
}

// projectWith builds a compiled project holding the zone described, through the
// real reader — so a renamed attribute fails here rather than passing against a
// struct this test filled in itself.
//
// THE INDEXES ARE THE PART THAT MATTERS. Project.Zones walks the index and not
// the object map, so a document with the zone in it and no link is a project
// that declares no zones — which is exactly what this test asserted against for
// one confusing run.
func projectWith(t *testing.T, zone map[string]any) *specs.Project {
	t.Helper()

	const projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	const zoneID = "QmRgVLmWTBpgTbCPnHRUvKGnHqHf7Yd7t8ZffKeRXPkRLL"

	raw, err := json.Marshal(map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			"zone": map[string]any{zoneID: zone},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/zone/" + zone["name"].(string) + "/links": []any{
				"branches/main/projects/" + projectID + "/zone/" + zoneID},
		},
	})
	require.NoError(t, err)

	p, err := specs.Load(raw)
	require.NoError(t, err)
	require.NotEmpty(t, p.Zones(), "the fixture declares a zone and the reader found none")
	return p
}

func zoning(t *testing.T, apex string, extra ...map[string]any) (*index, *held) {
	t.Helper()

	z := map[string]any{"name": "own", "fqdn": apex}
	for _, e := range extra {
		for k, v := range e {
			z[k] = v
		}
	}
	return &index{at: map[string][2]string{apex: {"shop", "prod"}}},
		&held{projects: map[string]*specs.Project{"shop/prod": projectWith(t, z)}}
}

func TestTheAdapterNeedsAnIndexAndProjects(t *testing.T) {
	i, h := zoning(t, "example.com")

	_, err := zone.New(nil, h, nil)
	require.ErrorContains(t, err, "no host index")

	_, err = zone.New(i, nil, nil)
	require.ErrorContains(t, err, "no projects")

	z, err := zone.New(i, h, nil)
	require.NoError(t, err)
	require.NotNil(t, z)
}

func TestTheWalkTriesTheNameThenItsParents(t *testing.T) {
	i, h := zoning(t, "example.com")
	z, err := zone.New(i, h, nil)
	require.NoError(t, err)

	apex, lines, err := z.Records(t.Context(), "mail.shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "example.com", apex)
	require.Empty(t, lines)

	require.Equal(t, []string{"mail.shop.example.com", "shop.example.com", "example.com"}, i.asked,
		"most specific first, and it stopped at the one that answered")
}

func TestTheWalkStopsAtTwoLabels(t *testing.T) {
	i, h := zoning(t, "example.com")
	z, err := zone.New(i, h, nil)
	require.NoError(t, err)

	_, _, err = z.Records(t.Context(), "nothing.invalid")
	require.NoError(t, err)
	require.Equal(t, []string{"nothing.invalid"}, i.asked,
		"a two-label name is asked whole and its last label is never asked alone -- "+
			"that is what stopping at two labels means")

	i.asked = nil
	_, _, err = z.Records(t.Context(), "com")
	require.NoError(t, err)
	require.Empty(t, i.asked, "a single label is not walked at all")
}

func TestAnUnreadableProjectStopsTheWalk(t *testing.T) {
	i, h := zoning(t, "example.com")
	h.err = errors.New("register unreachable")

	z, err := zone.New(i, h, nil)
	require.NoError(t, err)

	_, _, err = z.Records(t.Context(), "www.example.com")
	require.ErrorContains(t, err, "register unreachable")
	require.ErrorContains(t, err, "example.com", "and which apex it was reading for")
}

func TestAnIndexedNameThatIsNotAZoneKeepsWalking(t *testing.T) {
	i, h := zoning(t, "example.com")
	i.at["shop.example.com"] = [2]string{"shop", "prod"}

	z, err := zone.New(i, h, nil)
	require.NoError(t, err)

	apex, _, err := z.Records(t.Context(), "shop.example.com")
	require.NoError(t, err)
	require.Equal(t, "example.com", apex, "the zone above answered, not the gateway host")
	require.Equal(t, []string{"shop.example.com", "example.com"}, i.asked)
}

func TestANameNoZoneCoversAnswersNothing(t *testing.T) {
	i, h := zoning(t, "example.com")
	z, err := zone.New(i, h, nil)
	require.NoError(t, err)

	apex, lines, err := z.Records(t.Context(), "www.elsewhere.net")
	require.NoError(t, err)
	require.Empty(t, apex)
	require.Empty(t, lines)
}

func TestRecordsComeOffTheZoneThatCoversTheName(t *testing.T) {
	i, h := zoning(t, "example.com", map[string]any{
		"records": []any{"@ 3600 IN MX 10 mail.example.com.", "www 300 IN A 1.2.3.4"},
	})
	z, err := zone.New(i, h, nil)
	require.NoError(t, err)

	apex, lines, err := z.Records(t.Context(), "www.example.com")
	require.NoError(t, err)
	require.Equal(t, "example.com", apex)
	require.Equal(t, []string{"@ 3600 IN MX 10 mail.example.com.", "www 300 IN A 1.2.3.4"}, lines)
}

func TestTheResolverIsFoundAsAnyOtherFunction(t *testing.T) {
	i, h := zoning(t, "example.com", map[string]any{
		"resolver": map[string]any{"name": "warren", "namespace": "/billing/dns"},
	})
	h.spec = invoke.Spec{Name: "warren", Artifact: "bafywarren"}
	g := &guest{answer: &invoke.Resolution{Exists: true, Records: []string{"geo 60 IN A 203.0.113.9"}}}

	z, err := zone.New(i, h, g)
	require.NoError(t, err)

	got, err := z.Resolve(t.Context(), invoke.Question{Name: "geo.example.com.", Type: "A"})
	require.NoError(t, err)
	require.Equal(t, []string{"geo 60 IN A 203.0.113.9"}, got.Records)

	require.Len(t, h.targets, 1)
	require.Equal(t, worker.Target{
		Project: "shop", Environment: "prod",
		Scope: []string{"billing", "dns"},
		Kind:  "function", Name: "warren",
	}, h.targets[0], "its own deployment, its own scope, and a function like any other")

	require.Equal(t, h.spec, g.specs[0], "asked through the spec the worker resolved")
	require.Equal(t, "geo.example.com.", g.asked[0].Name)
}

func TestNobodyIsAskedWhenThereIsNothingToAsk(t *testing.T) {
	t.Run("no zone covers it", func(t *testing.T) {
		i, h := zoning(t, "example.com", map[string]any{
			"resolver": map[string]any{"name": "warren"},
		})
		g := &guest{}
		z, err := zone.New(i, h, g)
		require.NoError(t, err)

		got, err := z.Resolve(t.Context(), invoke.Question{Name: "geo.elsewhere.net.", Type: "A"})
		require.NoError(t, err)
		require.Nil(t, got)
		require.Empty(t, g.asked)
	})

	t.Run("the zone names no resolver", func(t *testing.T) {
		i, h := zoning(t, "example.com")
		g := &guest{}
		z, err := zone.New(i, h, g)
		require.NoError(t, err)

		got, err := z.Resolve(t.Context(), invoke.Question{Name: "geo.example.com.", Type: "A"})
		require.NoError(t, err)
		require.Nil(t, got)
		require.Empty(t, g.asked)
		require.Empty(t, h.targets, "and the worker was not troubled either")
	})

	// THE CASE THAT MUST NOT BECOME NXDOMAIN. A node with no component tier
	// cannot run the resolver a zone names; answering absence would take every
	// name in that zone off the internet for as long as anybody cached it.
	t.Run("this node runs no component tier", func(t *testing.T) {
		i, h := zoning(t, "example.com", map[string]any{
			"resolver": map[string]any{"name": "warren"},
		})
		z, err := zone.New(i, h, nil)
		require.NoError(t, err)

		got, err := z.Resolve(t.Context(), invoke.Question{Name: "geo.example.com.", Type: "A"})
		require.NoError(t, err)
		require.Nil(t, got, "nobody was asked, which is not `absent`")
	})
}

func TestAResolverThatCannotBeFoundIsAnError(t *testing.T) {
	i, h := zoning(t, "example.com", map[string]any{
		"resolver": map[string]any{"name": "warren"},
	})
	h.specErr = errors.New("no such function")

	z, err := zone.New(i, h, &guest{})
	require.NoError(t, err)

	_, err = z.Resolve(t.Context(), invoke.Question{Name: "geo.example.com.", Type: "A"})
	require.ErrorContains(t, err, "no such function")
	require.ErrorContains(t, err, "warren", "named, so an operator knows which one")
	require.ErrorContains(t, err, "example.com", "and for which zone")
}

func TestAGuestThatFailedIsReportedAndNotSwallowed(t *testing.T) {
	i, h := zoning(t, "example.com", map[string]any{
		"resolver": map[string]any{"name": "warren"},
	})
	g := &guest{err: errors.New("the component would not start")}

	z, err := zone.New(i, h, g)
	require.NoError(t, err)

	_, err = z.Resolve(t.Context(), invoke.Question{Name: "geo.example.com.", Type: "A"})
	require.ErrorContains(t, err, "would not start",
		"the server decides what a failure means -- this reports it")
}

func TestAnUnreadableProjectStopsTheResolverToo(t *testing.T) {
	i, h := zoning(t, "example.com")
	h.err = errors.New("register unreachable")

	z, err := zone.New(i, h, &guest{})
	require.NoError(t, err)

	_, err = z.Resolve(t.Context(), invoke.Question{Name: "geo.example.com.", Type: "A"})
	require.ErrorContains(t, err, "register unreachable")
}
