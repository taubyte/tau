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

// A DESTINATION READS ITS OWN VIEW, an empty one is still a view, and a destination
// the deployment does not list has none.
func TestADestinationReadsItsView(t *testing.T) {
	r, _ := newRegister(t)
	publishCut(t, r, 1, map[string]string{"eu": "eu view", "us": "us view"})

	eu, err := r.ResolveView(ctx, "shop", "prod", "eu")
	require.NoError(t, err)
	require.Equal(t, IDOf([]byte("eu view")), eu.Object)
	require.EqualValues(t, 1, eu.Revision)
	body, err := r.Object(ctx, eu.Object)
	require.NoError(t, err)
	require.Equal(t, "eu view", string(body))

	m, id, err := r.Manifest(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, eu.Manifest, id)
	require.Equal(t, IDOf([]byte("whole 1")), m.Object, "the whole object travels with the views")
	require.False(t, m.Whole)
	require.Len(t, m.Views, 2)

	_, err = r.ResolveView(ctx, "shop", "prod", "ap")
	require.ErrorIs(t, err, ErrNoView)
	_, err = r.ResolveView(ctx, "shop", "staging", "eu")
	require.ErrorIs(t, err, ErrNotPublished)

	// the whole object is still what Resolve names, for whoever needs all of it
	p, err := r.Resolve(ctx, "shop", "prod")
	require.NoError(t, err)
	require.Equal(t, m.Object, p.Object)
}

// A DESTINATION LEARNS WHAT IS ITS BUSINESS FROM ITS OWN PREFIX, and stops holding a
// deployment withdrawn from it.
func TestADestinationLearnsItsAssignments(t *testing.T) {
	r, _ := newRegister(t)
	publishCut(t, r, 1, map[string]string{"eu": "a", "us": "b"})
	_, err := r.Publish(ctx, Publication{Project: "blog", Environment: "prod", Revision: 1, Compiled: []byte("blog"),
		Views: []View{{Destination: "us", Compiled: []byte("blog us")}}})
	require.NoError(t, err)

	names := func(as []Assignment) []string {
		var out []string
		for _, a := range as {
			out = append(out, a.Project+"/"+a.Environment)
		}
		return out
	}
	us, err := r.Assigned(ctx, "us")
	require.NoError(t, err)
	require.Equal(t, []string{"blog/prod", "shop/prod"}, names(us))
	eu, err := r.Assigned(ctx, "eu")
	require.NoError(t, err)
	require.Equal(t, []string{"shop/prod"}, names(eu))

	// REVISION 2 IS NOT IN us ANY MORE: its old row remains, and it is not listed.
	publishCut(t, r, 2, map[string]string{"eu": "a2"})
	us, err = r.Assigned(ctx, "us")
	require.NoError(t, err)
	require.Equal(t, []string{"blog/prod"}, names(us))
	_, err = r.ResolveView(ctx, "shop", "prod", "us")
	require.ErrorIs(t, err, ErrNoView)
	eu, err = r.Assigned(ctx, "eu")
	require.NoError(t, err)
	require.EqualValues(t, 2, eu[0].View.Revision)

	// AN EMPTY VIEW IS A VIEW: what it holds was removed, and the destination is told.
	publishCut(t, r, 3, map[string]string{"eu": "{}", "us": "{}"})
	us, err = r.Assigned(ctx, "us")
	require.NoError(t, err)
	require.Equal(t, []string{"blog/prod", "shop/prod"}, names(us))

	none, err := r.Assigned(ctx, "ap")
	require.NoError(t, err)
	require.Empty(t, none)
}

// WHAT HAS NOT ARRIVED IS WAITED FOR: a newer manifest whose view is missing, or a row
// whose manifest is missing, reads as the newest revision that is whole.
func TestAReaderToleratesWhatHasNotArrived(t *testing.T) {
	r, store := newRegister(t)
	publishCut(t, r, 1, map[string]string{"eu": "eu 1"})
	publishCut(t, r, 2, map[string]string{"eu": "eu 2"})

	// the view of revision 2 has not replicated here yet
	held := store.m
	delete(held, objectKey(IDOf([]byte("eu 2"))))
	v, err := r.ResolveView(ctx, "shop", "prod", "eu")
	require.NoError(t, err)
	require.EqualValues(t, 1, v.Revision)

	// a revision-3 row whose manifest has not arrived is not a revision yet
	held[manifestKey("shop", "prod", 3, "f00")] = nil
	v, err = r.ResolveView(ctx, "shop", "prod", "eu")
	require.NoError(t, err)
	require.EqualValues(t, 1, v.Revision)

	// nothing whole at all is not published, rather than an error
	delete(held, objectKey(IDOf([]byte("eu 1"))))
	_, err = r.ResolveView(ctx, "shop", "prod", "eu")
	require.ErrorIs(t, err, ErrNotPublished)
}

// TWO PUBLISHERS OF ONE REVISION WRITE TWO ENTRIES, and every reader picks the same one.
func TestConcurrentRevisionsResolveTheSameEverywhere(t *testing.T) {
	a, store := newRegister(t)
	b, err := New(WithStore(store))
	require.NoError(t, err)

	_, err = a.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Views: []View{{Destination: "eu", Compiled: []byte("from a")}}})
	require.NoError(t, err)
	// b's process holds no lock a's did, and a replicated store orders nothing: model a
	// second node publishing the same revision by writing beside a's entry, as it would.
	require.NoError(t, b.publishViews(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1,
		Views: []View{{Destination: "eu", Compiled: []byte("from b")}}}, IDOf([]byte("y"))))

	entries, err := a.entriesUnder(ctx, manifestPrefix+"shop/prod/")
	require.NoError(t, err)
	require.Len(t, entries, 2, "one key per entry: neither overwrote the other")
	va, err := a.ResolveView(ctx, "shop", "prod", "eu")
	require.NoError(t, err)
	vb, err := b.ResolveView(ctx, "shop", "prod", "eu")
	require.NoError(t, err)
	require.Equal(t, va, vb)
	require.Equal(t, entries[0].manifest, va.Manifest, "the lowest manifest id wins a tie")
}

// A PUBLICATION NOBODY CUT IS THE WHOLE OBJECT IN EVERY DESTINATION, indexed in none.
func TestAnUncutPublicationIsWholeEverywhere(t *testing.T) {
	r, _ := newRegister(t)
	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("all")})
	require.NoError(t, err)
	for _, d := range []string{"default", "eu"} {
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
	_, err = InRegion(r, "eu").Resolve(ctx, "shop", "nope")
	require.ErrorIs(t, err, ErrNotPublished)
}

func TestViewsRefuseWhatAddressesNothing(t *testing.T) {
	r, store := newRegister(t)
	for _, tc := range []struct {
		views []View
		want  error
	}{
		{[]View{{Destination: "", Compiled: []byte("x")}}, ErrNoDestination},
		{[]View{{Destination: "a/b", Compiled: []byte("x")}}, ErrSeparator},
		{[]View{{Destination: "eu"}}, ErrEmptyObject},
	} {
		_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"), Views: tc.views})
		require.ErrorIs(t, err, tc.want)
	}
	_, err := r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Views: []View{{Destination: "eu", Compiled: []byte("1")}, {Destination: "eu", Compiled: []byte("2")}}})
	require.ErrorContains(t, err, "two views for eu")

	for _, call := range []func() error{
		func() error { _, err := r.ResolveView(ctx, "", "prod", "eu"); return err },
		func() error { _, err := r.ResolveView(ctx, "shop", "", "eu"); return err },
		func() error { _, err := r.ResolveView(ctx, "shop", "prod", ""); return err },
		func() error { _, _, err := r.Manifest(ctx, "", "prod"); return err },
		func() error { _, _, err := r.Manifest(ctx, "shop", ""); return err },
		func() error { _, err := r.Assigned(ctx, ""); return err },
	} {
		require.Error(t, call())
	}

	// A MANIFEST THAT IS PRESENT AND UNREADABLE IS DATA LOSS, not "not published".
	store.m[objectKey("bad")] = []byte("manifest 1\nrevision x\n")
	store.m[manifestKey("shop", "prod", 9, "bad")] = nil
	_, err = r.ResolveView(ctx, "shop", "prod", "eu")
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

// A STORE THAT CANNOT ANSWER IS SAID AS SUCH, never as "nothing here".
func TestViewsSayAStoreThatFails(t *testing.T) {
	broken := failing{MemStore: NewMemStore()}
	r, err := New(WithStore(&broken))
	require.NoError(t, err)
	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"),
		Views: []View{{Destination: "eu", Compiled: []byte("v")}}})
	require.NoError(t, err)

	broken.list = true
	_, err = r.ResolveView(ctx, "shop", "prod", "eu")
	require.ErrorIs(t, err, errFailing)
	_, _, err = r.Manifest(ctx, "shop", "prod")
	require.ErrorIs(t, err, errFailing)
	_, err = r.Assigned(ctx, "eu")
	require.ErrorIs(t, err, errFailing)

	broken.list, broken.get = false, true
	_, err = r.ResolveView(ctx, "shop", "prod", "eu")
	require.ErrorIs(t, err, errFailing)
	_, _, err = r.Manifest(ctx, "shop", "prod")
	require.ErrorIs(t, err, errFailing)
	_, err = r.Assigned(ctx, "eu")
	require.ErrorIs(t, err, errFailing)

	broken.get, broken.put = false, true
	_, err = r.Publish(ctx, Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("x"),
		Views: []View{{Destination: "eu", Compiled: []byte("v2")}}})
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
