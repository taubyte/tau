package raft

import (
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"

	taupeer "github.com/taubyte/tau/p2p/peer"
)

// answerAll answers every call the way a healthy follower would, until the
// benchmark ends.
func answerAll(b *testing.B, far raft.Transport) {
	go func() {
		for {
			select {
			case rpc := <-far.Consumer():
				if cmd, ok := rpc.Command.(*raft.AppendEntriesRequest); ok {
					rpc.Respond(&raft.AppendEntriesResponse{Term: cmd.Term, LastLog: cmd.PrevLogEntry + uint64(len(cmd.Entries)), Success: true}, nil)
					continue
				}
				rpc.Respond(nil, nil)
			case <-b.Context().Done():
				return
			}
		}
	}()
}

func appendOf(entries int) *raft.AppendEntriesRequest {
	req := &raft.AppendEntriesRequest{
		RPCHeader: raft.RPCHeader{ProtocolVersion: raft.ProtocolVersionMax, ID: []byte("leader"), Addr: []byte("leader")},
		Term:      3, PrevLogEntry: 100, PrevLogTerm: 3, LeaderCommitIndex: 99,
	}
	for i := range entries {
		req.Entries = append(req.Entries, &raft.Log{Index: uint64(101 + i), Term: 3, Type: raft.LogCommand, Data: make([]byte, 256)})
	}
	return req
}

func BenchmarkTransport(b *testing.B) {
	near, far := benchTransports(b)
	answerAll(b, far)
	far.SetHeartbeatHandler(func(rpc raft.RPC) { rpc.Respond(&raft.AppendEntriesResponse{Success: true}, nil) })
	target := far.LocalAddr()

	b.Run("append16", func(b *testing.B) {
		args := appendOf(16)
		b.ReportAllocs()
		for b.Loop() {
			var resp raft.AppendEntriesResponse
			if err := near.AppendEntries(raft.ServerID(target), target, args, &resp); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("heartbeat", func(b *testing.B) {
		args := &raft.AppendEntriesRequest{RPCHeader: raft.RPCHeader{Addr: []byte("leader")}, Term: 3}
		b.ReportAllocs()
		for b.Loop() {
			var resp raft.AppendEntriesResponse
			if err := near.AppendEntries(raft.ServerID(target), target, args, &resp); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("pipeline16", func(b *testing.B) {
		p, err := near.AppendEntriesPipeline(raft.ServerID(target), target)
		if err != nil {
			b.Fatal(err)
		}
		defer p.Close()
		args := appendOf(16)
		b.ReportAllocs()
		for b.Loop() {
			var resp raft.AppendEntriesResponse
			if _, err := p.AppendEntries(args, &resp); err != nil {
				b.Fatal(err)
			}
			if err := (<-p.Consumer()).Error(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// benchTransports is the group transport on two linked mock nodes.
func benchTransports(b *testing.B) (raft.Transport, raft.Transport) {
	a, f := taupeer.Mock(b.Context()), taupeer.Mock(b.Context())
	b.Cleanup(a.Close)
	b.Cleanup(f.Close)
	if err := taupeer.LinkAllPeers(); err != nil {
		b.Fatal(err)
	}
	if err := a.Peer().Connect(b.Context(), peer.AddrInfo{ID: f.ID(), Addrs: f.Peer().Addrs()}); err != nil {
		b.Fatal(err)
	}
	near, err := newNamespaceTransport(a.Peer(), "bench", 5*time.Second, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { near.Close() })
	far, err := newNamespaceTransport(f.Peer(), "bench", 5*time.Second, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { far.Close() })
	return near, far
}
