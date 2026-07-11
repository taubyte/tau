package hoarder

import (
	"fmt"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// ReplicasOf asks a hoarder for the live holder peers of a database/storage
// instance (registry claims crossed with the mesh). kind is accepted for API
// symmetry; the instance hash already names the resource uniquely.
func (c *Client) ReplicasOf(kind hoarderIface.ResourceKind, project, application, match string) ([]peerCore.ID, error) {
	resp, err := c.Send(hoarderSpecs.HoarderCommand, command.Body{
		hoarderSpecs.BodyAction:  hoarderSpecs.ActionReplicas,
		hoarderSpecs.BodyProject: project,
		hoarderSpecs.BodyApp:     application,
		hoarderSpecs.BodyMatch:   match,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("resolving replicas failed with: %w", err)
	}

	ids, err := maps.StringArray(resp, hoarderSpecs.BodyPeers)
	if err != nil {
		return nil, fmt.Errorf("reading replica peers failed with: %w", err)
	}

	peers := make([]peerCore.ID, 0, len(ids))
	for _, id := range ids {
		pid, err := peerCore.Decode(id)
		if err != nil {
			continue
		}
		peers = append(peers, pid)
	}
	return peers, nil
}
