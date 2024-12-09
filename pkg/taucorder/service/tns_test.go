package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/dream"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"gotest.tools/v3/assert"

	_ "github.com/taubyte/tau/dream/fixtures"
	"github.com/taubyte/tau/pkg/config-compiler/decompile"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	_ "github.com/taubyte/tau/services/tns"
)

func TestTNS(t *testing.T) {
	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	s, err := getMockService(ctx)
	assert.NilError(t, err)

	ns := &nodeService{Service: s}
	ts := &tnsService{Service: s}

	s.addHandler(pbconnect.NewNodeServiceHandler(ns))

	uname := t.Name()
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	assert.NilError(t, u.StartWithConfig(&dream.Config{
		Services: map[string]common.ServiceConfig{"tns": {Others: map[string]int{"copies": 2}}},
		Simples: map[string]dream.SimpleConfig{
			"client": {Clients: map[string]*common.ClientConfig{"tns": {}}},
		},
	}))

	project, err := decompile.MockBuild(testProjectId, "",
		&structureSpec.Library{
			Id:   testLibraryId,
			Name: "someLibrary",
			Path: "/",
		},
		&structureSpec.Function{
			Id:      testFunctionId,
			Name:    "someFunc",
			Type:    "http",
			Call:    "ping",
			Memory:  100000,
			Timeout: 1000000000,
			Method:  "GET",
			Source:  "libraries/someLibrary",
			Domains: []string{"someDomain"},
			Paths:   []string{"/ping"},
		},
		&structureSpec.Domain{
			Id:   testDomainId,
			Name: "someDomain",
			Fqdn: "hal.computers.com",
		},
		&structureSpec.Website{
			Id:      testWebsiteId,
			Name:    "someWebsite",
			Domains: []string{"someDomain"},
			Paths:   []string{"/"},
		},
	)
	if err != nil {
		t.Error(err)
		return
	}

	assert.NilError(t, u.RunFixture("injectProject", project))

	time.Sleep(10 * time.Second)

	//wait for nodes sync
	n1, err := u.Simple("client")
	assert.NilError(t, err)
	n1tns, err := n1.TNS()
	assert.NilError(t, err)

	tnsNodes := u.AllTNS()
	n1tns1 := n1tns.Peers(tnsNodes[0].Node().ID())
	n1tns2 := n1tns.Peers(tnsNodes[1].Node().ID())

	for {
		time.Sleep(time.Second)
		st1 := n1tns1.Stats()
		st2 := n1tns2.Stats()
		kv1, _ := st1.Database()
		kv2, _ := st2.Database()

		if slices.EqualFunc(kv1.Heads(), kv2.Heads(), func(i, j cid.Cid) bool { return i.String() == j.String() }) {
			fmt.Println("states:", kv1.Heads(), kv2.Heads())
			break
		}
	}

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

	hpath, handler := pbconnect.NewTNSServiceHandler(ts)

	mux := http.NewServeMux()
	mux.Handle(hpath, handler)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	go func() {
		server.Serve(listener)
	}()
	defer server.Shutdown(ctx)

	t.Parallel()

	t.Run("List", func(t *testing.T) {
		c := pbconnect.NewTNSServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		stream, err := c.List(ctx, connect.NewRequest(&pb.TNSListRequest{Node: ni.Msg, Depth: 2}))
		assert.NilError(t, err)

		all := make([]string, 0)
		for stream.Receive() {
			assert.NilError(t, stream.Err())
			tnsPath := stream.Msg().GetLeafs()
			all = append(all, path.Join(tnsPath...))
		}

		assert.Equal(t, slices.Contains(all, "projects/QmegMKBQmDTU9FUGKdhPFn1ZEtwcNaCA2wmyLW8vJn7wZN"), true)
		assert.Equal(t, slices.Contains(all, "libraries/QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt"), true)
		assert.Equal(t, slices.Contains(all, "domains"), true)
		assert.Equal(t, len(all), 15)
	})

	t.Run("Fetch", func(t *testing.T) {
		c := pbconnect.NewTNSServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		obj, err := c.Fetch(ctx, connect.NewRequest(&pb.TNSFetchRequest{
			Node: ni.Msg,
			Path: &pb.TNSPath{
				Leafs: []string{"branches", "main", "commit", "testCommit", "projects", testProjectId, "functions", testFunctionId},
			},
		}))
		assert.NilError(t, err)

		assert.Equal(
			t,
			obj.Msg.GetJson(),
			`{"call":"ping","description":"","domains":["QmNxpVc6DnbR3MKuvb3xw8Jzb8pfJTSRWJEdBMsb8AXFEX"],"memory":100000,"method":"GET","name":"someFunc","paths":["/ping"],"secure":false,"source":"libraries/QmP6qBNyoLeMLiwk8uYZ8xoT4CnDspYntcY4oCkpVG1byt","timeout":1000000000,"type":"http"}`,
		)
	})

	t.Run("Lookup", func(t *testing.T) {
		c := pbconnect.NewTNSServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		obj, err := c.Lookup(ctx, connect.NewRequest(&pb.TNSLookupRequest{
			Node: ni.Msg,
			Match: &pb.TNSLookupRequest_Prefix{
				Prefix: &pb.TNSPath{
					Leafs: []string{"branches", "main", "commit", "testCommit", "projects", testProjectId, "functions", testFunctionId},
				},
			},
		}))
		assert.NilError(t, err)

		assert.Equal(t, len(obj.Msg.GetPaths()), 11)
	})

	t.Run("Lookup Regex", func(t *testing.T) {
		c := pbconnect.NewTNSServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		obj, err := c.Lookup(ctx, connect.NewRequest(&pb.TNSLookupRequest{
			Node: ni.Msg,
			Match: &pb.TNSLookupRequest_Regex{
				Regex: &pb.TNSPath{
					Leafs: []string{"branches", "main", "commit", "testCommit", "projects", testProjectId, ".*", testWebsiteId},
				},
			},
		}))
		assert.NilError(t, err)

		assert.Equal(t, len(obj.Msg.GetPaths()), 8)
	})

	t.Run("State", func(t *testing.T) {
		c := pbconnect.NewTNSServiceClient(http.DefaultClient, "http://"+listener.Addr().String())
		for _, n := range u.AllTNS() {
			pid := n.Node().ID().String()
			cstate, err := c.State(ctx, connect.NewRequest(&pb.ConsensusStateRequest{
				Node: ni.Msg,
				Pid:  pid,
			}))
			assert.NilError(t, err)
			assert.Equal(t, cstate.Msg.GetMember().GetId(), pid)
			assert.Equal(t, len(cstate.Msg.GetCrdt().GetHeads()) > 0, true) // should be empty
		}
	})

	t.Run("States", func(t *testing.T) {
		c := pbconnect.NewTNSServiceClient(http.DefaultClient, "http://"+listener.Addr().String())

		pids := make([]string, 0, len(tnsNodes))
		for _, n := range tnsNodes {
			pids = append(pids, n.Node().ID().String())
		}

		sstream, err := c.States(ctx, connect.NewRequest(ni.Msg))
		assert.NilError(t, err)

		count := 0
		for sstream.Receive() {
			msg := sstream.Msg()
			assert.Equal(t, slices.Contains(pids, msg.GetMember().GetId()), true)
			assert.Equal(t, len(msg.GetCrdt().GetHeads()) > 0, true)
			count++
		}

		assert.Equal(t, count, len(tnsNodes))
	})
}
