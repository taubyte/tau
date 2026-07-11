package hoarder

import (
	"context"
	"fmt"
	"io"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

func (srv *Service) setupStreamRoutes() {
	srv.stream.Define("ping", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"time": int(time.Now().Unix())}, nil
	})
	srv.stream.Define(hoarderSpecs.HoarderCommand, srv.ServiceHandler)
	srv.stream.Define(hoarderSpecs.KVDBCommand, srv.kvdbHandler)
	srv.stream.DefineStream(hoarderSpecs.StashCommand, srv.stashReady, srv.stashReceive)
}

// ServiceHandler routes the classic command actions (observability only now —
// data lands via the stash stream, not this command).
func (srv *Service) ServiceHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	action, err := maps.String(body, hoarderSpecs.BodyAction)
	if err != nil {
		return nil, fmt.Errorf("getting action failed with: %w", err)
	}

	switch action {
	case hoarderSpecs.ActionRare:
		return srv.rareHandler(ctx)
	case hoarderSpecs.ActionList:
		return srv.listHandler(ctx)
	case hoarderSpecs.ActionReplicas:
		return srv.replicasHandler(ctx, body)
	case hoarderSpecs.ActionStatus:
		return srv.statusHandler(ctx, body)
	case hoarderSpecs.ActionLoad:
		return srv.loadHandler(ctx, body)
	case hoarderSpecs.ActionUnload:
		return srv.unloadHandler(body)
	case hoarderSpecs.ActionMetas:
		return srv.metasHandler(ctx, body)
	case hoarderSpecs.ActionStashStatus:
		return srv.stashStatusHandler(ctx, body)
	}

	return nil, fmt.Errorf("action %s unknown", action)
}

// metasHandler resolves instance hashes to their placement identity records
// (absent hashes are omitted). Read-only: a node that knows only a data-path
// hash recovers the instance's (kind, project, app, match, branch) here.
func (srv *Service) metasHandler(ctx context.Context, body command.Body) (cr.Response, error) {
	hashes, err := maps.StringArray(body, hoarderSpecs.BodyHashes)
	if err != nil {
		return nil, fmt.Errorf("missing hashes: %w", err)
	}
	out := make(map[string]interface{}, len(hashes))
	for _, h := range hashes {
		meta, err := srv.getMeta(ctx, h)
		if err != nil {
			continue
		}
		out[h] = map[string]interface{}{
			hoarderSpecs.BodyKind:    int(meta.Kind),
			hoarderSpecs.BodyConfig:  meta.ConfigId,
			hoarderSpecs.BodyProject: meta.ProjectId,
			hoarderSpecs.BodyApp:     meta.ApplicationId,
			hoarderSpecs.BodyMatch:   meta.Match,
			hoarderSpecs.BodyBranch:  meta.Branch,
		}
	}
	return cr.Response{hoarderSpecs.BodyMetas: out}, nil
}

// stashStatusHandler reports the LIVE stash claim count per requested CID
// (0 when unknown) plus the current fleet-clamped stash target. Read-only: a
// byte holder checks a CID reached its replica target before dropping its
// local copy — claims by dead peers don't count toward durability, and the
// holder can't compute the target itself (it doesn't see the hoarder fleet).
func (srv *Service) stashStatusHandler(ctx context.Context, body command.Body) (cr.Response, error) {
	cids, err := maps.StringArray(body, hoarderSpecs.BodyCids)
	if err != nil {
		return nil, fmt.Errorf("missing cids: %w", err)
	}
	out := make(map[string]interface{}, len(cids))
	for _, cid := range cids {
		claims, err := srv.listStashClaims(ctx, cid)
		if err != nil {
			out[cid] = 0
			continue
		}
		out[cid] = len(srv.liveClaimants(claims))
	}
	return cr.Response{
		hoarderSpecs.BodyClaims: out,
		hoarderSpecs.BodyTarget: stashTarget(srv.fleetSize()),
	}, nil
}

// instanceHashFromBody derives the instance hash from the {project, app, match}
// carried by an ops request.
func instanceHashFromBody(body command.Body) string {
	return instanceHash(hoarderIface.MetaData{
		ProjectId:     maps.TryString(body, hoarderSpecs.BodyProject),
		ApplicationId: maps.TryString(body, hoarderSpecs.BodyApp),
		Match:         maps.TryString(body, hoarderSpecs.BodyMatch),
	})
}

// statusHandler reports registry claims, the live subset, and whether this node
// currently has the instance loaded — the raw truth for `replicas status`.
func (srv *Service) statusHandler(ctx context.Context, body command.Body) (cr.Response, error) {
	hash := instanceHashFromBody(body)
	claims, err := srv.listClaims(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("listing claims failed with: %w", err)
	}
	return cr.Response{
		"claims":               claims,
		hoarderSpecs.BodyPeers: srv.liveClaimants(claims),
		"loaded":               srv.isLoaded(hash),
	}, nil
}

// loadHandler explicitly loads an instance this node claims (tests / ops).
func (srv *Service) loadHandler(ctx context.Context, body command.Body) (cr.Response, error) {
	hash := instanceHashFromBody(body)
	if _, err := srv.load(hash); err != nil {
		return nil, fmt.Errorf("load failed with: %w", err)
	}
	return cr.Response{"loaded": true}, nil
}

// unloadHandler explicitly unloads an instance (claim persists).
func (srv *Service) unloadHandler(body command.Body) (cr.Response, error) {
	srv.unload(instanceHashFromBody(body))
	return cr.Response{"loaded": false}, nil
}

// replicasHandler resolves the live holder peers of a database/storage instance
// (registry claims crossed with the live mesh). Clients that aren't on the
// hoarder mesh (e.g. substrate) use this instead of resolving liveness locally.
func (srv *Service) replicasHandler(ctx context.Context, body command.Body) (cr.Response, error) {
	project := maps.TryString(body, hoarderSpecs.BodyProject)
	app := maps.TryString(body, hoarderSpecs.BodyApp)
	match := maps.TryString(body, hoarderSpecs.BodyMatch)

	hash := instanceHash(hoarderIface.MetaData{ProjectId: project, ApplicationId: app, Match: match})
	claims, err := srv.listClaims(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("listing claims for %s failed with: %w", hash, err)
	}
	return cr.Response{hoarderSpecs.BodyPeers: srv.liveClaimants(claims)}, nil
}

// stashReady is the command phase of a push. OSS accepts unconditionally; the
// admission hooks slot in here.
func (srv *Service) stashReady(context.Context, streams.Connection, command.Body) (cr.Response, error) {
	return cr.Response{"ready": true}, nil
}

// stashReceive is the raw phase: read the framed header, import the streamed
// bytes, verify the CID matches what the sender declared (reject on mismatch,
// nothing kept), record the stash placement + this node's claim, fan out to
// co-claimants, then ack with the imported CID.
func (srv *Service) stashReceive(ctx context.Context, rw io.ReadWriter) {
	header, err := command.Decode(nil, rw)
	if err != nil {
		logger.Errorf("stash: decoding header failed with: %s", err)
		return
	}
	cidStr, err := maps.String(header.Body, hoarderSpecs.BodyCid)
	if err != nil {
		logger.Errorf("stash: missing cid in header: %s", err)
		return
	}
	target, _ := maps.Int(header.Body, hoarderSpecs.BodyTarget)
	owner := maps.TryString(header.Body, hoarderSpecs.BodyOwner)
	fanout, _ := maps.Bool(header.Body, hoarderSpecs.BodyFanout)

	gotCid, err := srv.node.AddFileForCid(rw)
	if err != nil {
		stashErr(rw, fmt.Sprintf("importing bytes failed: %s", err))
		return
	}
	if gotCid.String() != cidStr {
		// The bytes hash to something other than declared — do not keep them.
		srv.node.DeleteFile(gotCid.String()) //nolint:errcheck
		stashErr(rw, fmt.Sprintf("cid mismatch: declared %s, imported %s", cidStr, gotCid.String()))
		return
	}

	self := srv.node.ID().String()
	if err := srv.putStashMeta(ctx, cidStr, &StashMeta{Target: target, OwnerHash: owner}); err != nil {
		stashErr(rw, fmt.Sprintf("writing stash meta failed: %s", err))
		return
	}
	if err := srv.addStashClaim(ctx, cidStr, self); err != nil {
		stashErr(rw, fmt.Sprintf("claiming stash failed: %s", err))
		return
	}

	if fanout {
		srv.fanoutStash(ctx, cidStr, target, owner)
	}

	_ = cr.Response{hoarderSpecs.BodyCid: cidStr}.Encode(rw)
}

func stashErr(rw io.Writer, msg string) {
	logger.Error("stash: ", msg)
	_ = cr.Response{"error": msg}.Encode(rw)
}

// fanoutStash re-pushes freshly-stashed bytes from this node to up to target-1
// co-claimants (survivor-pushes-bytes). Re-pushes carry Fanout=false so the
// receivers don't fan out again.
func (srv *Service) fanoutStash(ctx context.Context, cid string, target int, owner string) {
	need := target - 1
	if need <= 0 || srv.stashClient == nil {
		return
	}

	self := srv.node.ID().String()

	pushed := 0
	for _, pidStr := range srv.activeMembers() {
		if pidStr == self {
			continue
		}
		pid, err := peerCore.Decode(pidStr)
		if err != nil {
			continue
		}
		f, err := srv.node.GetFile(ctx, cid)
		if err != nil {
			logger.Errorf("stash fan-out: reading %s failed with: %s", cid, err)
			continue
		}
		err = srv.stashClient.Peers(pid).Stash(cid, f,
			hoarderIface.WithTarget(target),
			hoarderIface.WithOwner(owner),
			hoarderIface.WithoutFanout())
		f.Close()
		if err != nil {
			logger.Errorf("stash fan-out to %s failed with: %s", pidStr, err)
			continue
		}
		if pushed++; pushed >= need {
			break
		}
	}
}

// stashClaimedByMe lists the stashed CIDs this node holds a claim on.
func (srv *Service) stashClaimedByMe(ctx context.Context) ([]string, error) {
	cids, err := srv.listStashCids(ctx)
	if err != nil {
		return nil, err
	}
	self := srv.node.ID().String()
	mine := make([]string, 0, len(cids))
	for _, cid := range cids {
		if _, ok := srv.stashClaimSince(ctx, cid, self); ok {
			mine = append(mine, cid)
		}
	}
	return mine, nil
}

func (srv *Service) listHandler(ctx context.Context) (cr.Response, error) {
	cids, err := srv.stashClaimedByMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("list failed with: %w", err)
	}
	return cr.Response{"ids": cids}, nil
}

// rareHandler reports the CIDs this node holds whose claimant count is below the
// replica target — observability only.
func (srv *Service) rareHandler(ctx context.Context) (cr.Response, error) {
	mine, err := srv.stashClaimedByMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("rare failed with: %w", err)
	}
	target := targetReplicas(srv.fleetSize())

	rare := make([]string, 0)
	for _, cid := range mine {
		claims, err := srv.listStashClaims(ctx, cid)
		if err != nil {
			continue
		}
		if len(claims) < target {
			rare = append(rare, cid)
		}
	}
	return cr.Response{"rare": rare}, nil
}
