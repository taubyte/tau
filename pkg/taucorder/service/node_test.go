package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/taubyte/tau/dream"
	taucorderv1 "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/utils"
)

func TestNode(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{}))

	t.Run("New node from config", func(t *testing.T) {
		n, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Cloud{
				Cloud: &taucorderv1.SporeDrive{
					ConfigId: test_valid_config_id,
				},
			},
		}))
		assert.NilError(t, err)

		assert.Equal(t, n.Msg.GetId() != "", true)
	})

	t.Run("New node from config (remote spore-drive)", func(t *testing.T) {
		_, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Cloud{
				Cloud: &taucorderv1.SporeDrive{
					ConfigId: test_valid_config_id,
					Connect:  &taucorderv1.Link{},
				},
			},
		}))
		assert.ErrorContains(t, err, "not implemented")
	})

	t.Run("New node from dream", func(t *testing.T) {
		_, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Universe{
				Universe: &taucorderv1.Dream{
					Universe: uname,
				},
			},
		}))
		assert.NilError(t, err)
	})

	t.Run("New node from dream (no bootstrap)", func(t *testing.T) {
		ni, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Universe{
				Universe: &taucorderv1.Dream{
					Universe:  uname,
					Bootstrap: &taucorderv1.Dream_Disable{Disable: true},
				},
			},
		}))
		assert.NilError(t, err)
		assert.Equal(t, len(s.nodes[ni.Msg.GetId()].Peer().Peerstore().Peers()), 1)
	})

	t.Run("New node from dream (bootstrap count 1)", func(t *testing.T) {
		ni, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Universe{
				Universe: &taucorderv1.Dream{
					Universe:  uname,
					Bootstrap: &taucorderv1.Dream_SubsetCount{SubsetCount: 1},
				},
			},
		}))
		assert.NilError(t, err)
		assert.Equal(t, len(s.nodes[ni.Msg.GetId()].Peer().Peerstore().Peers()), 2)
	})

	t.Run("New node from dream (bootstrap percentage 100%)", func(t *testing.T) {
		ni, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Universe{
				Universe: &taucorderv1.Dream{
					Universe:  uname,
					Bootstrap: &taucorderv1.Dream_SubsetPercentage{SubsetPercentage: 1},
				},
			},
		}))
		assert.NilError(t, err)
		assert.Equal(t, len(s.nodes[ni.Msg.GetId()].Peer().Peerstore().Peers()), 2)
	})

	t.Run("New node from dream (provided swarm key)", func(t *testing.T) {
		swarmKey, _ := utils.FormatSwarmKey(utils.GenerateSwarmKeyFromString(uname))
		_, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Universe{
				Universe: &taucorderv1.Dream{
					Universe: uname,
					SwarmKey: swarmKey,
				},
			},
		}))
		assert.NilError(t, err)
	})

	t.Run("New node from raw", func(t *testing.T) {
		swarmKey, _ := utils.FormatSwarmKey(utils.GenerateSwarmKeyFromString(uname))
		snode, err := u.Simple("elder")
		assert.NilError(t, err)
		_, err = ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Raw{
				Raw: &taucorderv1.Raw{
					SwarmKey: swarmKey,
					Peers:    []string{snode.Node.Peer().Addrs()[0].String() + "/p2p/" + snode.Node.ID().String()},
				},
			},
		}))
		assert.NilError(t, err)
	})

	t.Run("New node from raw (no swarm key)", func(t *testing.T) {
		snode, err := u.Simple("elder")
		assert.NilError(t, err)
		_, err = ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Raw{
				Raw: &taucorderv1.Raw{
					Peers: []string{snode.Node.Peer().Addrs()[0].String() + "/p2p/" + snode.Node.ID().String()},
				},
			},
		}))
		assert.NilError(t, err)
	})

	t.Run("New node from raw (bad swarm key)", func(t *testing.T) {
		snode, err := u.Simple("elder")
		assert.NilError(t, err)
		_, err = ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Raw{
				Raw: &taucorderv1.Raw{
					SwarmKey: []byte(""),
					Peers:    []string{snode.Node.Peer().Addrs()[0].String() + "/p2p/" + snode.Node.ID().String()},
				},
			},
		}))
		assert.ErrorContains(t, err, "not correctly formatted")
	})

	t.Run("Free node", func(t *testing.T) {
		ni, err := ns.New(ctx, connect.NewRequest(&taucorderv1.Config{
			Source: &taucorderv1.Config_Universe{
				Universe: &taucorderv1.Dream{
					Universe: uname,
				},
			},
		}))
		assert.NilError(t, err)

		_, err = ns.Free(ctx, connect.NewRequest(&taucorderv1.Node{Id: ni.Msg.GetId()}))
		assert.NilError(t, err)
	})
}
