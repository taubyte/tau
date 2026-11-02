package register

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func publishCut(t *testing.T, r *Register, rev uint64, views map[string]string) Publication {
	t.Helper()
	p := Publication{Project: "shop", Environment: "prod", Revision: rev, Compiled: []byte("whole " + string(rune('0'+rev))), Views: []View{}}
	for d, body := range views {
		p.Views = append(p.Views, View{Destination: d, Compiled: []byte(body)})
	}
	_, err := r.Publish(ctx, p)
	require.NoError(t, err)
	return p
}

func TestADestinationReadsItsView(t *testing.T) {
	r, _ := newRegister(t)
	publishCut(t, r, 1, map[string]string{"region-1": "region-1 view", "region-2": "region-2 view"})

	r1, err := r.ResolveView(ctx, "shop", "prod", "region-1")
	require.NoError(t, err)
	require.Equal(t, IDOf([]byte("region-1 view")), r1.Object)
	require.EqualValues(t, 1, r1.Revision)
	body, err := r.Object(ctx, r1.Object)
	require.NoError(t, err)
	require.Equal(t, "region-1 view", string(body))

	m, id, err := r.Manifest(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, r1.Manifest, id)
	require.Equal(t, IDOf([]byte("whole 1")), m.Object, "the whole object travels with the views")
	require.False(t, m.Whole)
	require.Len(t, m.Views, 2)

	_, err = r.ResolveView(ctx, "shop", "prod", "region-3")
	require.ErrorIs(t, err, ErrNoView)
	_, err = r.ResolveView(ctx, "shop", "staging", "region-1")
	require.ErrorIs(t, err, ErrNotPublished)

	// the whole object is still what Resolve names, for whoever needs all of it
	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, m.Object, p.Object)
}

func TestADestinationLearnsItsAssignments(t *testing.T) {
	r, _ := newRegister(t)
	publishCut(t, r, 1, map[string]string{"region-1": "a", "region-2": "b"})
	_, err := r.Publish(ctx, Publication{Project: "blog", Environment: "prod", Revision: 1, Compiled: []byte("blog"),
		Views: []View{{Destination: "region-2", Compiled: []byte("blog region-2")}}})
	require.NoError(t, err)

	names := func(as []Assignment) []string {
		var out []string
		for _, a := range as {
			out = append(out, a.Project+"/"+a.Environment)
		}
		return out
	}
	r2, err := r.Assigned(ctx, "region-2")
	require.NoError(t, err)
	require.Equal(t, []string{"blog/prod", "shop/prod"}, names(r2))
	r1, err := r.Assigned(ctx, "region-1")
	require.NoError(t, err)
	require.Equal(t, []string{"shop/prod"}, names(r1))

	// Revision 2 is not in region-2 any more: its old row remains, and it is not listed.
	publishCut(t, r, 2, map[string]string{"region-1": "a2"})
	r2, err = r.Assigned(ctx, "region-2")
	require.NoError(t, err)
	require.Equal(t, []string{"blog/prod"}, names(r2))
	_, err = r.ResolveView(ctx, "shop", "prod", "region-2")
	require.ErrorIs(t, err, ErrNoView)
	r1, err = r.Assigned(ctx, "region-1")
	require.NoError(t, err)
	require.EqualValues(t, 2, r1[0].View.Revision)

	// AN EMPTY VIEW IS A VIEW: what it holds was removed, and the destination is told.
	publishCut(t, r, 3, map[string]string{"region-1": "{}", "region-2": "{}"})
	r2, err = r.Assigned(ctx, "region-2")
	require.NoError(t, err)
	require.Equal(t, []string{"blog/prod", "shop/prod"}, names(r2))

	none, err := r.Assigned(ctx, "region-3")
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestAReaderToleratesWhatHasNotArrived(t *testing.T) {
	r, store := newRegister(t)
	publishCut(t, r, 1, map[string]string{"region-1": "region-1 1"})
	publishCut(t, r, 2, map[string]string{"region-1": "region-1 2"})

	// the view of revision 2 has not replicated here yet
	held := store.m
	delete(held, objectKey(IDOf([]byte("region-1 2"))))
	v, err := r.ResolveView(ctx, "shop", "prod", "region-1")
	require.NoError(t, err)
	require.EqualValues(t, 1, v.Revision)

	// a revision-3 row whose manifest has not arrived is not a revision yet
	held[manifestKey("shop", "prod", 3, "f00")] = nil
	v, err = r.ResolveView(ctx, "shop", "prod", "region-1")
	require.NoError(t, err)
	require.EqualValues(t, 1, v.Revision)

	// nothing whole at all is not published, rather than an error
	delete(held, objectKey(IDOf([]byte("region-1 1"))))
	_, err = r.ResolveView(ctx, "shop", "prod", "region-1")
	require.ErrorIs(t, err, ErrNotPublished)
}

func TestConcurrentRevisionsResolveTheSameEverywhere(t *testing.T) {
	a, store := newRegister(t)
	b, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = a.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Views: []View{{Destination: "region-1", Compiled: []byte("from a")}}})
	require.NoError(t, err)
	// b's process holds no lock a's did, and a replicated store orders nothing: model a
	// second node publishing the same revision by writing beside a's entry, as it would.
	require.NoError(t, b.publishViews(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1,
		Views: []View{{Destination: "region-1", Compiled: []byte("from b")}}}, IDOf([]byte("y"))))

	entries, err := a.entriesUnder(ctx, manifestPrefix+"shop/prod/")
	require.NoError(t, err)
	require.Len(t, entries, 2, "one key per entry: neither overwrote the other")
	va, err := a.ResolveView(ctx, "shop", "prod", "region-1")
	require.NoError(t, err)
	vb, err := b.ResolveView(ctx, "shop", "prod", "region-1")
	require.NoError(t, err)
	require.Equal(t, va, vb)
	require.Equal(t, entries[0].manifest, va.Manifest, "the lowest manifest id wins a tie")
}

func TestAnUncutPublicationIsWholeEverywhere(t *testing.T) {
	r, _ := newRegister(t)
	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("all")})
	require.NoError(t, err)
	for _, d := range []string{"default", "region-1"} {
		v, err := r.ResolveView(ctx, "shop", "prod", d)
		require.NoError(t, err)
		require.Equal(t, IDOf([]byte("all")), v.Object)
	}
	none, err := r.Assigned(ctx, "default")
	require.NoError(t, err)
	require.Empty(t, none)

	// THROUGH THE CACHE, the way every consumer reads.
	reg := InRegion(r, "default")
	p, err := reg.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	body, err := reg.Object(ctx, p.Object)
	require.NoError(t, err)
	require.Equal(t, "all", string(body))
	_, err = InRegion(r, "region-1").Resolve(ctx, "shop", "nope")
	require.ErrorIs(t, err, ErrNotPublished)
}

func TestViewsRefuseWhatAddressesNothing(t *testing.T) {
	r, store := newRegister(t)
	type testCase struct {
		views []View
		want  error
	}
	for _, tc := range []testCase{
		{[]View{{Destination: "", Compiled: []byte("x")}}, ErrNoDestination},
		{[]View{{Destination: "a/b", Compiled: []byte("x")}}, ErrSeparator},
		{[]View{{Destination: "region-1"}}, ErrEmptyObject},
	} {
		_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"), Views: tc.views})
		require.ErrorIs(t, err, tc.want)
	}
	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Views: []View{{Destination: "region-1", Compiled: []byte("1")}, {Destination: "region-1", Compiled: []byte("2")}}})
	require.ErrorContains(t, err, "two views for region-1")

	for _, call := range []func() error{
		func() error { _, err := r.ResolveView(ctx, "", "prod", "region-1"); return err },
		func() error { _, err := r.ResolveView(ctx, "shop", "", "region-1"); return err },
		func() error { _, err := r.ResolveView(ctx, "shop", "prod", ""); return err },
		func() error { _, _, err := r.Manifest(ctx, "", "prod"); return err },
		func() error { _, _, err := r.Manifest(ctx, "shop", ""); return err },
		func() error { _, err := r.Assigned(ctx, ""); return err },
	} {
		require.Error(t, call())
	}

	// A manifest that is present and unreadable is data loss, not "not published".
	store.m[objectKey("bad")] = []byte("manifest 1\nrevision x\n")
	store.m[manifestKey("shop", "prod", 9, "bad")] = nil
	_, err = r.ResolveView(ctx, "shop", "prod", "region-1")
	require.ErrorIs(t, err, ErrMalformedPointer)
	_, _, err = r.Manifest(ctx, "shop", "prod")
	require.ErrorIs(t, err, ErrMalformedPointer)
}

func TestManifestsRoundTrip(t *testing.T) {
	m := Manifest{Project: "p", Environment: "e", Revision: 7, Object: "o", Views: map[string]ObjectID{"b": "2", "a": "1"}}
	got, err := decodeManifest(encodeManifest(m))
	require.NoError(t, err)
	require.Equal(t, m, got)
	m.Whole, m.Views = true, map[string]ObjectID{}
	got, err = decodeManifest(encodeManifest(m))
	require.NoError(t, err)
	require.Equal(t, m, got)

	for _, bad := range []string{"", "nope", "manifest 1\nview a\n", "manifest 1\nwhat\n", "manifest 1\nproject p\n", "manifest 1\nrevision -1\nobject o\n"} {
		_, err := decodeManifest([]byte(bad))
		require.ErrorIs(t, err, ErrMalformedPointer, "%q", bad)
	}
	require.True(t, strings.HasPrefix(string(encodeManifest(m)), "manifest 1\n"))
}

func TestViewsSayAStoreThatFails(t *testing.T) {
	broken := failing{MemStore: NewMemStore()}
	r, err := New(WithStore(&broken))
	require.NoError(t, err)
	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Views: []View{{Destination: "region-1", Compiled: []byte("v")}}})
	require.NoError(t, err)

	broken.list = true
	_, err = r.ResolveView(ctx, "shop", "prod", "region-1")
	require.ErrorIs(t, err, errFailing)
	_, _, err = r.Manifest(ctx, "shop", "prod")
	require.ErrorIs(t, err, errFailing)
	_, err = r.Assigned(ctx, "region-1")
	require.ErrorIs(t, err, errFailing)

	broken.list, broken.get = false, true
	_, err = r.ResolveView(ctx, "shop", "prod", "region-1")
	require.ErrorIs(t, err, errFailing)
	_, _, err = r.Manifest(ctx, "shop", "prod")
	require.ErrorIs(t, err, errFailing)
	_, err = r.Assigned(ctx, "region-1")
	require.ErrorIs(t, err, errFailing)

	broken.get, broken.put = false, true
	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("x"),
		Views: []View{{Destination: "region-1", Compiled: []byte("v2")}}})
	require.ErrorIs(t, err, errFailing)
}

var errFailing = errors.New("store unreachable")

type failing struct {
	*MemStore
	get, put, list bool
}

func (f *failing) Get(c context.Context, key string) ([]byte, bool, error) {
	if f.get {
		return nil, false, errFailing
	}
	return f.MemStore.Get(c, key)
}

func (f *failing) Put(c context.Context, key string, v []byte) error {
	if f.put {
		return errFailing
	}
	return f.MemStore.Put(c, key, v)
}

func (f *failing) List(c context.Context, prefix string) ([]string, error) {
	if f.list {
		return nil, errFailing
	}
	return f.MemStore.List(c, prefix)
}
