package hoarder

import (
	"fmt"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams/command"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// Metas resolves instance hashes to their placement identity records; hashes
// with no record are omitted from the result.
func (c *Client) Metas(hashes ...string) ([]hoarderIface.InstanceInfo, error) {
	resp, err := c.Send(hoarderSpecs.HoarderCommand, command.Body{
		hoarderSpecs.BodyAction: hoarderSpecs.ActionMetas,
		hoarderSpecs.BodyHashes: hashes,
	}, c.peers...)
	if err != nil {
		return nil, fmt.Errorf("resolving metas failed with: %w", err)
	}

	raw := maps.SafeInterfaceToStringKeys(resp[hoarderSpecs.BodyMetas])
	out := make([]hoarderIface.InstanceInfo, 0, len(raw))
	for hash, entry := range raw {
		m := maps.SafeInterfaceToStringKeys(entry)
		kind, err := maps.Int(m, hoarderSpecs.BodyKind)
		if err != nil {
			continue
		}
		out = append(out, hoarderIface.InstanceInfo{
			Hash: hash,
			Kind: hoarderIface.ResourceKind(kind),
			Meta: hoarderIface.MetaData{
				ConfigId:      maps.TryString(m, hoarderSpecs.BodyConfig),
				ProjectId:     maps.TryString(m, hoarderSpecs.BodyProject),
				ApplicationId: maps.TryString(m, hoarderSpecs.BodyApp),
				Match:         maps.TryString(m, hoarderSpecs.BodyMatch),
				Branch:        maps.TryString(m, hoarderSpecs.BodyBranch),
			},
		})
	}
	return out, nil
}

// StashStatus reports the live stash claim count per CID (0 = unknown) and the
// fleet-clamped stash replica target.
func (c *Client) StashStatus(cids ...string) (map[string]int, int, error) {
	resp, err := c.Send(hoarderSpecs.HoarderCommand, command.Body{
		hoarderSpecs.BodyAction: hoarderSpecs.ActionStashStatus,
		hoarderSpecs.BodyCids:   cids,
	}, c.peers...)
	if err != nil {
		return nil, 0, fmt.Errorf("resolving stash status failed with: %w", err)
	}

	raw := maps.SafeInterfaceToStringKeys(resp[hoarderSpecs.BodyClaims])
	out := make(map[string]int, len(raw))
	for cid, v := range raw {
		n, err := maps.Int(map[string]interface{}{"n": v}, "n")
		if err != nil {
			continue
		}
		out[cid] = n
	}
	target, err := maps.Int(resp, hoarderSpecs.BodyTarget)
	if err != nil {
		return nil, 0, fmt.Errorf("reading stash target failed with: %w", err)
	}
	return out, target, nil
}
