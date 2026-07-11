package hoarder

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	ifaceTns "github.com/taubyte/tau/core/services/tns"
	spec "github.com/taubyte/tau/pkg/specs/common"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// assetSweepLoop ensures every build asset published in TNS is stash-
// replicated. An asset key (`assets/<hash>` → CID) can exist without stash
// claims — the build predates this data plane, every claimant was lost, or
// every push failed while the fleet was unreachable — leaving the bytes on
// whichever node happens to hold them, un-replicated. The sweep adopts such
// CIDs: fetch the bytes, pin, claim, fan out to the replica target.
// Additive-only: it never deletes anything. Runs once after boot (membership
// must form first so the one-adopter-per-CID selection is stable), then on a
// slow recurring interval as the self-heal for later gaps.
func (srv *Service) assetSweepLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * hoarderSpecs.LivenessTimeout):
	}

	for {
		srv.assetSweepOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(hoarderSpecs.AssetSweepInterval):
		}
	}
}

// assetSweepOnce runs one sweep, retrying a failing TNS listing a bounded
// number of times.
func (srv *Service) assetSweepOnce(ctx context.Context) {
	for attempt := 0; attempt < hoarderSpecs.AssetSweepRetries; attempt++ {
		if ctx.Err() != nil {
			return
		}
		cids, err := srv.tnsAssetCids()
		if err == nil {
			srv.adoptAssets(ctx, cids)
			return
		}
		logger.Errorf("asset sweep: listing TNS assets failed with: %s", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(hoarderSpecs.AssetSweepRetryInterval):
		}
	}
	logger.Error("asset sweep: TNS unreachable, retrying next sweep")
}

// tnsAssetCids lists every asset CID recorded in TNS: keys under `assets/`,
// each holding the CID string the build published.
func (srv *Service) tnsAssetCids() ([]string, error) {
	keysIface, err := srv.tnsClient.Lookup(ifaceTns.Query{Prefix: []string{"assets"}})
	if err != nil {
		return nil, err
	}
	keys, ok := keysIface.([]string)
	if !ok {
		return nil, fmt.Errorf("unexpected lookup result type %T", keysIface)
	}

	cids := make([]string, 0, len(keys))
	for _, k := range keys {
		parts := strings.Split(strings.TrimPrefix(k, "/"), "/")
		if len(parts) != 2 || parts[0] != "assets" {
			continue
		}
		obj, err := srv.tnsClient.Fetch(spec.NewTnsPath(parts))
		if err != nil {
			logger.Errorf("asset sweep: fetching %s failed with: %s", k, err)
			continue
		}
		if cid, ok := obj.Interface().(string); ok && cid != "" {
			cids = append(cids, cid)
		}
	}
	return dedup(cids), nil
}

// adoptAssets stash-replicates every CID whose live claim count is below the
// stash target. One adopter per CID (deterministic selection over live
// membership) so the fleet doesn't race-fetch the same bytes; claims/meta
// writes are idempotent regardless.
func (srv *Service) adoptAssets(ctx context.Context, cids []string) (adopted, skipped int) {
	self := srv.node.ID().String()
	for _, cid := range cids {
		if ctx.Err() != nil {
			return
		}
		members := srv.activeMembers()
		target := stashTarget(len(members))
		if claims, err := srv.listStashClaims(ctx, cid); err == nil && len(srv.liveClaimants(claims)) >= target {
			skipped++
			continue
		}
		if want := placementDesired(cid, members, 1); len(want) == 0 || want[0] != self {
			continue
		}
		if err := srv.adoptAsset(ctx, cid, target); err != nil {
			// Loud on purpose: no reachable holder means this asset is already
			// unservable cloud-wide until its resource is rebuilt.
			logger.Errorf("asset sweep: adopting %s failed with: %s", cid, err)
			continue
		}
		adopted++
	}
	if adopted > 0 || skipped > 0 {
		logger.Infof("asset sweep: adopted %d asset(s), %d already replicated", adopted, skipped)
	}
	return
}

// adoptAsset pulls the CID's bytes (bitswap, from whichever node holds them)
// into the local blockstore, records the stash placement + this node's claim,
// and fans out to co-claimants.
func (srv *Service) adoptAsset(ctx context.Context, cid string, target int) error {
	fctx, cancel := context.WithTimeout(ctx, hoarderSpecs.AssetSweepFetchTimeout)
	defer cancel()
	f, err := srv.node.GetFile(fctx, cid)
	if err != nil {
		return fmt.Errorf("fetching bytes failed with: %w", err)
	}
	_, err = io.Copy(io.Discard, f) // reading pulls the full DAG locally
	f.Close()
	if err != nil {
		return fmt.Errorf("reading bytes failed with: %w", err)
	}

	if err := srv.putStashMeta(ctx, cid, &StashMeta{Target: target}); err != nil {
		return err
	}
	if err := srv.addStashClaim(ctx, cid, srv.node.ID().String()); err != nil {
		return err
	}
	srv.fanoutStash(ctx, cid, target, "")
	return nil
}

// stashTarget is the system-calculated stash replica target: the stash default
// clamped to the live fleet, so single-node clouds converge to 1.
func stashTarget(liveFleet int) int {
	return max(min(hoarderSpecs.DefaultStashReplicas, liveFleet), 1)
}
