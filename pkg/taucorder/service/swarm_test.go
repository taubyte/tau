//go:build dreaming

package service

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/dream/api"
	"github.com/taubyte/tau/p2p/peer"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	srvcommon "github.com/taubyte/tau/services/common"
	_ "github.com/taubyte/tau/services/seer/dream"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"
)

func TestSwarm_Dreaming(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	dream.DreamApiPort = 32421 // don't conflict with default port
	m, err := dream.New(t.Context())
	assert.NilError(t, err)
	defer m.Close()

	uname := t.Name()
	u, err := m.New(dream.UniverseConfig{Name: uname})
	assert.NilError(t, err)

	assert.NilError(t, api.BigBang(m))

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ss := &swarmService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	assert.NilError(t, u.StartWithConfig(&dream.Config{Services: map[string]common.ServiceConfig{"seer": {}}, Simples: map[string]dream.SimpleConfig{"client": {}}}))

	ni, err := ns.New(ctx, connect.NewRequest(&pb.Config{
		Source: &pb.Config_Universe{
			Universe: &pb.Dream{
				Universe: uname,
			},
		},
	}))
	assert.NilError(t, err)

	defer ns.Free(ctx, connect.NewRequest(ni.Msg))

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	path, handler := pbconnect.NewSwarmServiceHandler(ss)

	mux := http.NewServeMux()
	mux.Handle(path, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Run("Connect peer", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		p, err := c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)
		assert.Equal(t, p.Msg.GetId(), tp.ID().String())
		assert.DeepEqual(t, p.Msg.GetAddresses(), []string{tpma.String()})
	})

	t.Run("Discover service", func(t *testing.T) {
		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		// seer advertises "/seer/v1" through the DHT (discoveryUtil.Advertise) and
		// Discover is a best-effort, point-in-time snapshot of currently
		// discoverable providers (libp2p FindProviders) — the proto count is a
		// max, not a target. Propagation is eventually consistent, so a single
		// shot races under load (count observed as 0). Poll until the one seer
		// provider converges. The deadline must clear the node's BackoffDiscovery
		// window: once a namespace lookup returns nothing, re-queries are
		// suppressed for minBackoff (60s), so a shorter deadline could not recover
		// from a missed first lookup.
		var count int
		deadline := time.Now().Add(90 * time.Second)
		for {
			count = 0
			pstream, err := c.Discover(ctx, connect.NewRequest(&pb.DiscoverRequest{
				Node:    ni.Msg,
				Service: srvcommon.SeerProtocol,
			}))
			if err == nil {
				for pstream.Receive() {
					count++
				}
				pstream.Close()
			}
			if count == 1 || time.Now().After(deadline) {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		assert.Equal(t, count, 1)
	})

	t.Run("List peers (no ping)", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_2", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		pstream, err := c.List(ctx, connect.NewRequest(&pb.ListRequest{
			Node: ni.Msg,
		}))
		assert.NilError(t, err)
		count := 0
		hit := false
		for pstream.Receive() {
			msg := pstream.Msg()
			count++
			if msg.GetId() == tp.ID().String() {
				hit = true
			}
		}
		assert.Equal(t, hit, true)
		assert.Equal(t, count > 1, true)
	})

	t.Run("List peers (with ping)", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_3", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		pstream, err := c.List(ctx, connect.NewRequest(&pb.ListRequest{
			Node: ni.Msg,
			Ping: &pb.ListPingRequest{
				Count:       3,
				Concurrency: 4,
			},
		}))
		assert.NilError(t, err)
		count := 0
		hit := false
		for pstream.Receive() {
			msg := pstream.Msg()
			count++
			if msg.GetId() == tp.ID().String() {
				hit = true
				assert.Equal(t, msg.GetPingStatus().GetUp(), true)
				assert.Equal(t, int(msg.GetPingStatus().GetCountTotal()), 3)
				assert.Equal(t, int(msg.GetPingStatus().GetCount()), 3)

				// Latency is the average RTT (peer node Ping: sum/healthy) of the
				// three pings above, plumbed through List -> ni.Ping -> PingStatus.
				// Assert it is a real, measured, per-peer value -- positive and a
				// sane in-window duration -- not that the box is fast. The RTT is an
				// in-process libp2p round-trip dominated by goroutine scheduling, so
				// a wall-clock "< 5ms" bound tests the host's scheduler and fails
				// whenever the machine is loaded; worse, "< 5ms" also holds at
				// latency 0, so it never caught an unmeasured/zero-value latency --
				// the one thing this field must prove. A healthy ping's RTT is
				// bounded by peer.PingTimeout, and Count == 3 above means all three
				// were healthy, so their average is < peer.PingTimeout on any box.
				latency := time.Duration(msg.GetPingStatus().GetLatency())
				assert.Equal(t, latency > 0, true)
				assert.Equal(t, latency < peer.PingTimeout, true)
			}
		}
		assert.Equal(t, hit, true)
		assert.Equal(t, count > 1, true)
	})

	t.Run("Ping peer", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_4", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		ret, err := c.Ping(ctx, connect.NewRequest(&pb.PingRequest{
			Node:  ni.Msg,
			Pid:   tp.ID().String(),
			Count: 5,
		}))

		assert.NilError(t, err)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCountTotal()), 5)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCount()), 5)
		assert.Equal(t, ret.Msg.GetPingStatus().GetUp(), true)

		// Same rationale as List peers (with ping): assert the latency is a real,
		// measured value (positive) and a sane in-window duration, not a wall-clock
		// "< 5ms" that only measures the host scheduler and also passes at 0. Count
		// == 5 above means all five pings were healthy, each RTT < peer.PingTimeout,
		// so their average is < peer.PingTimeout regardless of machine load.
		latency := time.Duration(ret.Msg.GetPingStatus().GetLatency())
		assert.Equal(t, latency > 0, true)
		assert.Equal(t, latency < peer.PingTimeout, true)
	})

	t.Run("Ping peer (timeout)", func(t *testing.T) {
		tp, err := u.CreateSimpleNode("test_peer_5", &dream.SimpleConfig{})
		assert.NilError(t, err)
		assert.NilError(t, tp.WaitForSwarm(3*time.Second))
		tpma := tp.Peer().Addrs()[0]

		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		_, err = c.Connect(ctx, connect.NewRequest(&pb.ConnectRequest{
			Node:    ni.Msg,
			Address: tpma.String() + "/p2p/" + tp.ID().String(),
		}))
		assert.NilError(t, err)

		ret, err := c.Ping(ctx, connect.NewRequest(&pb.PingRequest{
			Node:    ni.Msg,
			Pid:     tp.ID().String(),
			Count:   100000,
			Timeout: int64(5 * time.Millisecond),
		}))

		assert.NilError(t, err)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCountTotal()), 100000)
		assert.Equal(t, int(ret.Msg.GetPingStatus().GetCount()) < 100000, true)
		assert.Equal(t, ret.Msg.GetPingStatus().GetUp(), false)
	})

	t.Run("Wait for swarm", func(t *testing.T) {
		c := pbconnect.NewSwarmServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		_, err = c.Wait(ctx, connect.NewRequest(&pb.WaitRequest{
			Node:    ni.Msg,
			Timeout: int64(3 * time.Second),
		}))
		assert.NilError(t, err)
	})

}
