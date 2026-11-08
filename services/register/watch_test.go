package register

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	registerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/register/v2"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

// watching is a raw session on a harness: its id, and what its stream said.
type watching struct {
	id     string
	events chan *registerv2.WatchEvent
	ended  chan error
}

func watch(t *testing.T, h *harness) *watching {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	stream, err := h.Client.Watch(ctx, connect.NewRequest(&registerv2.WatchRequest{}))
	require.NoError(t, err)
	require.True(t, stream.Receive(), "the stream opens with its session: %v", stream.Err())
	ready := stream.Msg().GetReady()
	require.NotNil(t, ready)
	require.NotEmpty(t, ready.GetSession())
	w := &watching{id: ready.GetSession(), events: make(chan *registerv2.WatchEvent, 64), ended: make(chan error, 1)}
	go func() {
		for stream.Receive() {
			w.events <- stream.Msg()
		}
		w.ended <- stream.Err()
	}()
	return w
}

// changed is the next entries the session is told of, skipping beats.
func (w *watching) changed(t *testing.T) []uint64 {
	t.Helper()
	for {
		select {
		case e := <-w.events:
			if c := e.GetChanged(); c != nil {
				return c.GetEntries()
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the session was never told")
		}
	}
}

func (w *watching) quiet(t *testing.T, for_ time.Duration) {
	t.Helper()
	deadline := time.After(for_)
	for {
		select {
		case e := <-w.events:
			require.Nil(t, e.GetChanged(), "nothing it holds changed, and it was told %v", e.GetChanged().GetEntries())
		case <-deadline:
			return
		}
	}
}

func bindTo[T any](req *connect.Request[T], session string, entry uint64) *connect.Request[T] {
	req.Header().Set(sessionHeader, session)
	req.Header().Set(entryHeader, strconv.FormatUint(entry, 10))
	return req
}

func boundOf(t *testing.T, header http.Header, err error) bool {
	t.Helper()
	if err != nil {
		var ce *connect.Error
		require.True(t, errors.As(err, &ce))
		return ce.Meta().Get(boundHeader) == "1"
	}
	return header.Get(boundHeader) == "1"
}

func publishHost(t *testing.T, h *harness, project string, revision uint64, hosts ...string) {
	t.Helper()
	_, err := h.Service.reg.Publish(t.Context(), pkgregister.Publication{
		Project: project, Environment: "prod", Revision: revision, Compiled: []byte(project + strconv.FormatUint(revision, 10)), Hosts: hosts,
	})
	require.NoError(t, err)
}

func TestAMissIsBoundAndItsClaimNamesTheEntry(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	w := watch(t, h)

	_, err := h.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "shop.example.com"}), w.id, 7))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	require.True(t, boundOf(t, nil, err), "an absence is an answer a reader may keep")

	publishHost(t, h, "shop", 1, "shop.example.com")
	require.Equal(t, []uint64{7}, w.changed(t))

	// The binding fired and is released: a second change names nobody.
	publishHost(t, h, "shop", 2)
	w.quiet(t, 200*time.Millisecond)
}

func TestAnAnswerIsBoundAndAReadWithoutASessionIsNot(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	publishHost(t, h, "shop", 1, "shop.example.com")
	w := watch(t, h)

	res, err := h.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "Shop.Example.com."}), w.id, 1))
	require.NoError(t, err)
	require.Equal(t, "shop", res.Msg.GetProject())
	require.True(t, boundOf(t, res.Header(), nil))

	plain, err := h.Client.ResolveHost(t.Context(), connect.NewRequest(&registerv2.HostRequest{Fqdn: "shop.example.com"}))
	require.NoError(t, err)
	require.False(t, boundOf(t, plain.Header(), nil), "a read naming no session is kept by nobody")

	unknown, err := h.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "shop.example.com"}), "not-a-session", 1))
	require.NoError(t, err)
	require.False(t, boundOf(t, unknown.Header(), nil), "nor one naming a session this node does not hold")

	// Released by the next publish that moves it.
	publishHost(t, h, "blog", 1, "shop.example.com.blog")
	w.quiet(t, 100*time.Millisecond)
	_, err = h.Service.reg.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("x")})
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, w.changed(t), "dropping the name is a change to what it was read from")
}

func TestAForgottenEntryIsToldNothing(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	w := watch(t, h)
	for _, entry := range []uint64{1, 2} {
		_, err := h.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "shop.example.com"}), w.id, entry))
		require.True(t, boundOf(t, nil, err))
	}
	_, err := h.Client.Forget(t.Context(), connect.NewRequest(&registerv2.ForgetRequest{Session: w.id, Entries: []uint64{1}}))
	require.NoError(t, err)
	_, err = h.Client.Forget(t.Context(), connect.NewRequest(&registerv2.ForgetRequest{Session: "gone", Entries: []uint64{2}}))
	require.NoError(t, err, "forgetting on a session that is gone is nothing to do")

	publishHost(t, h, "shop", 1, "shop.example.com")
	require.Equal(t, []uint64{2}, w.changed(t))
}

func TestAViewIsToldWhenItsRevisionMoves(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	_, err := h.Service.reg.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("v1"),
		Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte("v1 region-1")}}})
	require.NoError(t, err)
	w := watch(t, h)

	res, err := h.Client.ResolveView(t.Context(), bindTo(connect.NewRequest(&registerv2.ViewRequest{Project: "shop", Environment: "prod", Destination: "region-1"}), w.id, 3))
	require.NoError(t, err)
	require.True(t, boundOf(t, res.Header(), nil))
	require.Equal(t, uint64(1), res.Msg.GetRevision())

	_, err = h.Service.reg.Publish(t.Context(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 2, Compiled: []byte("v2"),
		Views: []pkgregister.View{{Destination: "region-1", Compiled: []byte("v2 region-1")}}})
	require.NoError(t, err)
	require.Equal(t, []uint64{3}, w.changed(t))

	pointer, err := h.Client.Resolve(t.Context(), bindTo(connect.NewRequest(&registerv2.ResolveRequest{Project: "shop", Environment: "prod"}), w.id, 5))
	require.NoError(t, err)
	require.True(t, boundOf(t, pointer.Header(), nil))
}

func TestChangesThatComeTogetherAreToldTogether(t *testing.T) {
	h := newHarness(t, pkgregister.NewMemStore())
	w := watch(t, h)
	var names []string
	for i := range 50 {
		name := "s" + strconv.Itoa(i) + ".example.com"
		names = append(names, name)
		_, err := h.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: name}), w.id, uint64(i)))
		require.True(t, boundOf(t, nil, err))
	}
	publishHost(t, h, "shop", 1, names...)

	var told []uint64
	for len(told) < 50 {
		told = append(told, w.changed(t)...)
	}
	require.Len(t, told, 50)
	require.ElementsMatch(t, told, func() []uint64 {
		out := make([]uint64, 50)
		for i := range out {
			out[i] = uint64(i)
		}
		return out
	}())
}

func TestAFullSessionBindsNoMore(t *testing.T) {
	limits := defaultWatchLimits
	limits.entries = 1
	mn := newHarness(t, pkgregister.NewMemStore(), withWatchLimits(limits))
	w := watch(t, mn)

	_, err := mn.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "a.example.com"}), w.id, 1))
	require.True(t, boundOf(t, nil, err))
	_, err = mn.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "b.example.com"}), w.id, 2))
	require.False(t, boundOf(t, nil, err), "past its limit an answer comes back, kept by nobody")
	_, err = mn.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "c.example.com"}), w.id, 1))
	require.True(t, boundOf(t, nil, err), "an entry bound again replaces what it held")
}

func TestAReadThatCouldNotLookIsNotBound(t *testing.T) {
	h := newHarness(t, brokenStore{pkgregister.NewMemStore()})
	w := watch(t, h)
	_, err := h.Client.ResolveHost(t.Context(), bindTo(connect.NewRequest(&registerv2.HostRequest{Fqdn: "shop.example.com"}), w.id, 1))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
	require.False(t, boundOf(t, nil, err), "an unreachable store is no answer to keep")
	require.Zero(t, h.Service.watches.bound(mustSession(t, h, w.id)))
}

func mustSession(t *testing.T, h *harness, id string) *session {
	t.Helper()
	s, ok := h.Service.watches.find(id)
	require.True(t, ok)
	return s
}

func TestAQuietSessionBeats(t *testing.T) {
	limits := defaultWatchLimits
	limits.beat = 20 * time.Millisecond
	h := newHarness(t, pkgregister.NewMemStore(), withWatchLimits(limits))
	w := watch(t, h)
	select {
	case e := <-w.events:
		require.NotNil(t, e.GetBeat())
	case <-time.After(5 * time.Second):
		t.Fatal("a quiet session never beat")
	}
}

func TestAChangeThatCannotBeQueuedDropsEverySession(t *testing.T) {
	w := newWatches(watchLimits{entries: 8, beat: time.Hour, flush: time.Millisecond, batch: 8, queue: 1})
	t.Cleanup(w.close)
	a, b := w.open(), w.open()
	require.True(t, w.bind(a, 1, pkgregister.Depends{Keys: []string{"h/a"}}))
	require.True(t, w.bind(b, 1, pkgregister.Depends{Prefixes: []string{"m/shop/prod/"}, Keys: []string{"m/shop/prod/r/x"}}))
	require.Equal(t, pkgregister.Depends{Prefixes: []string{"m/shop/prod/"}}, b.entries[1], "a key its own prefix holds is not held twice")

	w.lost.Store(true)
	w.changed("h/other")
	for _, s := range []*session{a, b} {
		select {
		case <-s.gone:
		case <-time.After(5 * time.Second):
			t.Fatal("a session survived a lost change")
		}
	}
	require.False(t, w.bind(a, 2, pkgregister.Depends{Keys: []string{"h/a"}}), "and binds nothing after")
	w.mu.Lock()
	defer w.mu.Unlock()
	require.Empty(t, w.keys)
	require.Empty(t, w.prefixes)
}
