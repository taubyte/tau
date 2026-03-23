package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/fxamacker/cbor/v2"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/utils/maps"
)

// listClustersFromKVDB returns cluster names found under /cluster/ (excluding our own).
func (srv *PatrickService) listClustersFromKVDB(ctx context.Context) ([]string, error) {
	keys, err := srv.db.List(ctx, "/cluster/")
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, key := range keys {
		parts := strings.Split(strings.TrimPrefix(key, "/"), "/")
		if len(parts) < 2 || parts[0] != "cluster" {
			continue
		}
		clusterName := parts[1]
		if clusterName == "" || clusterName == srv.cluster {
			continue
		}
		seen[clusterName] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	return out, nil
}

type clusterPidPayload struct {
	Pid       string `cbor:"pid"`
	Timestamp int64  `cbor:"timestamp"`
}

// getClusterPeerID returns one peer ID registered for the given cluster (from /cluster/<name>/pid).
func (srv *PatrickService) getClusterPeerID(ctx context.Context, clusterName string) (peerCore.ID, error) {
	data, err := srv.db.Get(ctx, "/cluster/"+clusterName+"/pid")
	if err != nil {
		return "", err
	}
	var p clusterPidPayload
	if err := cbor.Unmarshal(data, &p); err != nil {
		return "", err
	}
	if p.Pid == "" {
		return "", fmt.Errorf("empty pid for cluster %s", clusterName)
	}
	return peerCore.Decode(p.Pid)
}

// checkJobAcrossClusters returns true if any other cluster's Patrick has this job (queued or stored).
func (srv *PatrickService) checkJobAcrossClusters(ctx context.Context, jobID string) (bool, error) {
	if srv.outboundClient == nil {
		return false, nil
	}
	clusters, err := srv.listClustersFromKVDB(ctx)
	if err != nil {
		return false, err
	}
	for _, name := range clusters {
		pid, err := srv.getClusterPeerID(ctx, name)
		if err != nil {
			continue // skip unreachable or missing cluster
		}
		resp, err := srv.outboundClient.Send("patrick", command.Body{"action": "hasJob", "jid": jobID}, pid)
		if err != nil {
			continue
		}
		has, err := maps.Bool(resp, "has")
		if err == nil && has {
			return true, nil
		}
	}
	return false, nil
}

// Cross-cluster dedup: call before enqueuing a job. Returns true if job was found in another cluster.
// jobExistsInOtherCluster returns true if the job is already queued or stored in another cluster.
func (srv *PatrickService) jobExistsInOtherCluster(ctx context.Context, jobID string) (bool, error) {
	return srv.checkJobAcrossClusters(ctx, jobID)
}
