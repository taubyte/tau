package register

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/peer/peertest"
	pkgregister "github.com/taubyte/tau/pkg/register"
)

func BenchmarkAKeptAnswer(b *testing.B) {
	mn := peertest.Net(b)
	registers, _, reader := connected(b, mn, 1)
	v := viewsOn(reader, ids(registers), defaultWatcherLimits, watchedOnly)
	b.Cleanup(func() { v.Close() })
	v.w.pick()
	require.Eventually(b, func() bool {
		v.w.mu.Lock()
		defer v.w.mu.Unlock()
		return len(v.w.live) == 1
	}, 30*time.Second, 10*time.Millisecond)
	ctx := context.Background()
	_, _, err := v.ResolveHost(ctx, "shop.example.com")
	require.ErrorIs(b, err, pkgregister.ErrNoHost)

	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			v.ResolveHost(ctx, "shop.example.com")
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				v.ResolveHost(ctx, "shop.example.com")
			}
		})
	})
}

func BenchmarkABoundRead(b *testing.B) {
	mn := mocknet.New()
	b.Cleanup(func() { mn.Close() })
	s, err := build(newHost(b, mn), withStore(pkgregister.NewMemStore()))
	require.NoError(b, err)
	b.Cleanup(func() { s.Close() })
	_, err = s.reg.Publish(context.Background(), pkgregister.Publication{Project: "shop", Environment: "prod", Revision: 1, Compiled: []byte("x"), Hosts: []string{"shop.example.com"}})
	require.NoError(b, err)
	session := s.watches.open()
	ctx := context.Background()
	read := func(r *pkgregister.Register) (string, error) {
		p, _, err := r.ResolveHost(ctx, "shop.example.com")
		return p, err
	}

	b.Run("unbound", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			boundRead(s, http.Header{}, read)
		}
	})
	b.Run("bound", func(b *testing.B) {
		h := http.Header{}
		h.Set(sessionHeader, session.id)
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			h.Set(entryHeader, strconv.Itoa(i%1024))
			boundRead(s, h, read)
			i++
		}
	})
}

func BenchmarkAChangeAgainstManyBindings(b *testing.B) {
	w := newWatches(watchLimits{entries: 1 << 20, beat: time.Hour, flush: time.Millisecond, batch: 1024, queue: 1 << 14})
	b.Cleanup(w.close)
	s := w.open()
	for i := range 10_000 {
		require.True(b, w.bind(s, uint64(i), pkgregister.Depends{
			Keys:     []string{"h/s" + strconv.Itoa(i) + ".example.com"},
			Prefixes: []string{"m/p" + strconv.Itoa(i%100) + "/prod/"},
		}))
	}
	b.ReportAllocs()
	for b.Loop() {
		w.fire("h/nobody.example.com")
		w.fire("m/other/prod/00000000000000000001/x")
	}
}
