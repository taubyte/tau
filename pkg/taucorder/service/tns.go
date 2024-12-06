package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/taubyte/tau/core/services/tns"
	specs "github.com/taubyte/tau/pkg/specs/common"
	pb "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1"
	"github.com/taubyte/tau/services/common"
)

type jsonArray []interface{}
type jsonMap map[string]interface{}

func (m jsonMap) Value() (interface{}, error) {
	return json.Marshal(m)
}

func recursiveAnyToStringKeys(v interface{}) (r interface{}) {
	switch v := v.(type) {
	case []interface{}:
		for i, e := range v {
			v[i] = recursiveAnyToStringKeys(e)
		}
		r = jsonArray(v)
	case map[interface{}]interface{}:
		newMap := make(map[string]interface{}, len(v))
		for k, e := range v {
			newMap[k.(string)] = recursiveAnyToStringKeys(e)
		}
		r = jsonMap(newMap)
	default:
		r = v
	}
	return
}

func (ts *tnsService) Fetch(ctx context.Context, req *connect.Request[pb.TNSFetchRequest]) (*connect.Response[pb.TNSObject], error) {
	ni, err := ts.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	reqPath := req.Msg.GetPath()
	if reqPath == nil || len(reqPath.GetLeafs()) == 0 {
		return nil, errors.New("can't fetch root")
	}

	tnsPath := specs.NewTnsPath(reqPath.GetLeafs())

	ret, err := ni.tnsClient.Fetch(tnsPath)
	if err != nil {
		return nil, fmt.Errorf("fetching `%s` failed: %w", tnsPath.String(), err)
	}

	jsonRet, err := json.Marshal(recursiveAnyToStringKeys(ret.Interface()))
	if err != nil {
		return nil, fmt.Errorf("encoding failed: %w", err)
	}

	return connect.NewResponse(&pb.TNSObject{
		Path: reqPath,
		Json: string(jsonRet),
	}), nil
}

func (ts *tnsService) List(ctx context.Context, req *connect.Request[pb.TNSListRequest], stream *connect.ServerStream[pb.TNSPath]) error {
	ni, err := ts.getNode(req.Msg)
	if err != nil {
		return err
	}

	depth := req.Msg.GetDepth()
	if depth < 1 {
		return errors.New("invalid depth value")
	}

	keys, err := ni.tnsClient.List(int(depth))
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}

	for _, key := range keys {
		err = stream.Send(&pb.TNSPath{
			Leafs: key,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (ts *tnsService) Lookup(ctx context.Context, req *connect.Request[pb.TNSLookupRequest]) (*connect.Response[pb.TNSPaths], error) {
	ni, err := ts.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	var query tns.Query
	switch v := req.Msg.GetMatch().(type) {
	case *pb.TNSLookupRequest_Regex:
		query.Prefix = v.Regex.Leafs
		query.RegEx = true
	case *pb.TNSLookupRequest_Prefix:
		query.Prefix = v.Prefix.Leafs
	}

	ret, err := ni.tnsClient.Lookup(query)
	if err != nil {
		return nil, fmt.Errorf("lookup failed: %w", err)
	}

	keys, ok := ret.([]string)
	if !ok {
		return nil, errors.New("lookup returned invalid type")
	}

	paths := make([]*pb.TNSPath, 0, len(keys))
	for _, k := range keys {
		paths = append(paths, &pb.TNSPath{
			Leafs: strings.Split(k, "/")[1:],
		})
	}

	return connect.NewResponse(&pb.TNSPaths{
		Paths: paths,
	}), nil
}

func (ts *tnsService) State(cyx context.Context, req *connect.Request[pb.ConsensusStateRequest]) (*connect.Response[pb.ConsensusState], error) {
	ni, err := ts.getNode(req.Msg)
	if err != nil {
		return nil, err
	}

	spid := req.Msg.GetPid()
	if spid == "" {
		return nil, errors.New("empty peer id")
	}

	pid, err := peer.Decode(spid)
	if err != nil {
		return nil, fmt.Errorf("decoding peer id: %w", err)
	}

	sts, err := ni.tnsClient.Peers(pid).Stats().Database()
	if err != nil {
		return nil, fmt.Errorf("fetching database state: %w", err)
	}

	heads := make([]string, 0, len(sts.Heads()))
	for _, cid := range sts.Heads() {
		heads = append(heads, cid.String())
	}

	return connect.NewResponse(&pb.ConsensusState{
		Member: &pb.Peer{
			Id: spid,
		},
		Consensus: &pb.ConsensusState_Crdt{
			Crdt: &pb.CRDTState{
				Heads: heads,
			},
		},
	}), nil
}

func (ts *tnsService) States(ctx context.Context, req *connect.Request[pb.Node], stream *connect.ServerStream[pb.ConsensusState]) error {
	ni, err := ts.getNodeById(req.Msg.GetId())
	if err != nil {
		return err
	}

	for _, pid := range ni.Peer().Peerstore().Peers() {
		protos, err := ni.Peer().Peerstore().GetProtocols(pid)
		if err == nil && slices.Contains(protos, protocol.ID(common.TnsProtocol)) {
			sts, err := ni.tnsClient.Peers(pid).Stats().Database()
			if err == nil {
				heads := make([]string, 0, len(sts.Heads()))
				for _, cid := range sts.Heads() {
					heads = append(heads, cid.String())
				}

				stream.Send(&pb.ConsensusState{
					Member: &pb.Peer{
						Id: pid.String(),
					},
					Consensus: &pb.ConsensusState_Crdt{
						Crdt: &pb.CRDTState{
							Heads: heads,
						},
					},
				})
			}
		}
	}

	return nil
}
