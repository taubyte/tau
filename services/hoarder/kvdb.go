package hoarder

import (
	"context"
	"fmt"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/kvdb"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	"github.com/taubyte/tau/utils/maps"
)

// kvdbHandler is the remote data plane. It resolves the instance this node
// should serve (first-touching an unplaced resource, or redirecting the client
// to the live replicas), then applies the requested kv operation to the loaded
// instance kvdb.
func (srv *Service) kvdbHandler(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
	handle, hash, redirect, err := srv.resolveInstance(ctx, body)
	if err != nil {
		return nil, err
	}
	if redirect != nil {
		return redirect, nil
	}

	op, err := maps.String(body, hoarderSpecs.BodyKVOp)
	if err != nil {
		return nil, fmt.Errorf("missing kv op: %w", err)
	}

	var resp cr.Response
	switch op {
	case hoarderSpecs.KVGet:
		resp, err = srv.kvGet(ctx, handle, body)
	case hoarderSpecs.KVPut:
		resp, err = srv.kvPut(ctx, handle, hash, body)
	case hoarderSpecs.KVDelete:
		resp, err = srv.kvDelete(ctx, handle, hash, body)
	case hoarderSpecs.KVList:
		resp, err = srv.kvList(ctx, handle, body)
	case hoarderSpecs.KVListRegex:
		resp, err = srv.kvListRegex(ctx, handle, body)
	case hoarderSpecs.KVBatch:
		resp, err = srv.kvBatch(ctx, handle, hash, body)
	case hoarderSpecs.KVSync:
		resp, err = srv.kvSync(ctx, handle, body)
	default:
		return nil, fmt.Errorf("unknown kv op %q", op)
	}
	if err != nil {
		return nil, err
	}
	// Tell the client which node served it so it can pin for read-your-writes.
	resp[hoarderSpecs.BodyServedBy] = srv.node.ID().String()
	return resp, nil
}

// resolveInstance decides this node's role for the requested instance using
// deterministic HRW placement — no auction:
//   - the resource is unknown → record its meta and notify the fleet;
//   - this node is one of the HRW-desired owners → claim + load + serve;
//   - otherwise → not-replica redirect to the desired owners (which self-claim
//     when the client's redirected request reaches them).
func (srv *Service) resolveInstance(ctx context.Context, body command.Body) (kvdb.KVDB, string, cr.Response, error) {
	auction, err := auctionFromBody(body)
	if err != nil {
		return nil, "", nil, err
	}
	hash := instanceHash(auction.Meta)
	self := srv.node.ID().String()

	// A K=2 barrier replication push (no-barrier flag) targets this node because
	// it is a chosen holder in the registry claim set — persist it directly and
	// never redirect. Redirecting would drop the durable second copy (a momentary
	// HRW/membership divergence must not cost an acked write). Ensure we are a
	// loaded claimant (idempotent) and serve.
	if nb, _ := maps.Bool(body, hoarderSpecs.BodyNoBarrier); nb {
		if !srv.isClaimed(hash) {
			if err := srv.claimAndLoad(ctx, hash, auction); err != nil {
				return nil, "", nil, fmt.Errorf("barrier claim of %s failed with: %w", hash, err)
			}
		}
		handle, err := srv.load(hash)
		if err != nil {
			return nil, "", nil, fmt.Errorf("loading %s failed with: %w", hash, err)
		}
		return handle, hash, nil, nil
	}

	// Ensure the resource is recorded so every node can place it. First writer
	// validates the config and notifies the fleet (idempotent, write-on-change).
	if _, err := srv.getMeta(ctx, hash); err != nil {
		if verr := srv.validateConfig(auction); verr != nil {
			return nil, "", nil, verr
		}
		meta := metaFromAuction(auction)
		if err := srv.putMeta(ctx, hash, meta); err != nil {
			return nil, "", nil, fmt.Errorf("recording meta for %s failed with: %w", hash, err)
		}
		srv.publishReconcile(ctx, hash, meta)
	}

	members := srv.activeMembers()
	target := targetReplicas(len(members))
	desired := placementDesired(hash, members, target)

	if contains(desired, self) {
		if !srv.isClaimed(hash) {
			if err := srv.claimAndLoad(ctx, hash, auction); err != nil {
				return nil, "", nil, fmt.Errorf("claiming %s failed with: %w", hash, err)
			}
			srv.publishReconcile(ctx, hash, metaFromAuction(auction))
		}
		handle, err := srv.load(hash)
		if err != nil {
			return nil, "", nil, fmt.Errorf("loading %s failed with: %w", hash, err)
		}
		return handle, hash, nil, nil
	}

	// Not an owner — redirect to the desired owners.
	return nil, "", cr.Response{
		hoarderSpecs.BodyCode:  hoarderSpecs.CodeNotReplica,
		hoarderSpecs.BodyPeers: desired,
	}, nil
}

// metaFromAuction builds the durable placement record from the request carrier.
func metaFromAuction(auction *hoarderIface.Auction) *RegistryMeta {
	return &RegistryMeta{
		Kind:          auction.MetaType,
		ConfigId:      auction.Meta.ConfigId,
		ProjectId:     auction.Meta.ProjectId,
		ApplicationId: auction.Meta.ApplicationId,
		Match:         auction.Meta.Match,
		Branch:        auction.Meta.Branch,
	}
}

func auctionFromBody(body command.Body) (*hoarderIface.Auction, error) {
	kind, err := maps.Int(body, hoarderSpecs.BodyKind)
	if err != nil {
		return nil, fmt.Errorf("missing kind: %w", err)
	}
	project, err := maps.String(body, hoarderSpecs.BodyProject)
	if err != nil {
		return nil, fmt.Errorf("missing project: %w", err)
	}
	match, err := maps.String(body, hoarderSpecs.BodyMatch)
	if err != nil {
		return nil, fmt.Errorf("missing match: %w", err)
	}
	return &hoarderIface.Auction{
		MetaType: hoarderIface.ResourceKind(kind),
		Meta: hoarderIface.MetaData{
			ProjectId:     project,
			ApplicationId: maps.TryString(body, hoarderSpecs.BodyApp),
			Match:         match,
			Branch:        maps.TryString(body, hoarderSpecs.BodyBranch),
		},
	}, nil
}

func (srv *Service) kvGet(ctx context.Context, handle kvdb.KVDB, body command.Body) (cr.Response, error) {
	key, err := maps.String(body, hoarderSpecs.BodyKey)
	if err != nil {
		return nil, err
	}
	value, err := handle.Get(ctx, key)
	if err != nil {
		return cr.Response{hoarderSpecs.BodyCode: hoarderSpecs.CodeNotFound}, nil
	}
	plain, err := srv.cipherDecrypt(value)
	if err != nil {
		return nil, fmt.Errorf("decrypting value failed with: %w", err)
	}
	return cr.Response{hoarderSpecs.BodyValue: plain}, nil
}

func (srv *Service) kvPut(ctx context.Context, handle kvdb.KVDB, hash string, body command.Body) (cr.Response, error) {
	key, err := maps.String(body, hoarderSpecs.BodyKey)
	if err != nil {
		return nil, err
	}
	value, err := maps.ByteArray(body, hoarderSpecs.BodyValue)
	if err != nil {
		return nil, fmt.Errorf("missing value: %w", err)
	}
	if err := srv.admitWrite(maps.TryString(body, hoarderSpecs.BodyProject), len(value)); err != nil {
		return cr.Response{hoarderSpecs.BodyCode: hoarderSpecs.CodeOverCapacity}, nil
	}
	enc, err := srv.cipherEncrypt(value)
	if err != nil {
		return nil, fmt.Errorf("encrypting value failed with: %w", err)
	}
	if err := handle.Put(ctx, key, enc); err != nil {
		return nil, fmt.Errorf("put failed with: %w", err)
	}
	srv.replicateWrite(ctx, hash, body)
	return cr.Response{}, nil
}

func (srv *Service) kvDelete(ctx context.Context, handle kvdb.KVDB, hash string, body command.Body) (cr.Response, error) {
	key, err := maps.String(body, hoarderSpecs.BodyKey)
	if err != nil {
		return nil, err
	}
	if err := handle.Delete(ctx, key); err != nil {
		return nil, fmt.Errorf("delete failed with: %w", err)
	}
	srv.replicateWrite(ctx, hash, body)
	return cr.Response{}, nil
}

// replicateWrite is the K=2 ack barrier: after the local commit, synchronously
// apply the same write on a co-claimant before the caller's ack, so an acked
// write exists on ≥2 nodes and survives this node's immediate death. It targets
// live co-claimants (registry claims crossed with membership) and retries
// transient stream failures until a co-claimant acks — a co-claimant that is a
// live member must receive the write, or an acked value could vanish if this node
// dies. It gives up (ack local-only, reconcile re-replicates) only when there is
// genuinely no live co-claimant (fleet=1), or a co-claimant stays undialable past
// the barrier window (see the loop). A push already carrying no-barrier is skipped
// (it *is* the second copy).
func (srv *Service) replicateWrite(ctx context.Context, hash string, body command.Body) {
	if nb, _ := maps.Bool(body, hoarderSpecs.BodyNoBarrier); nb {
		return
	}

	self := srv.node.ID().String()
	repl := command.Body{}
	for k, v := range body {
		repl[k] = v
	}
	repl[hoarderSpecs.BodyNoBarrier] = true

	// Strict K>=2 durability barrier. The second copy must land on a live
	// co-owner before we ack, or an acked value vanishes if this node then dies.
	//
	// The push set is the HRW-desired owners derived from *membership* — not the
	// CRDT claim registry alone, which lags a just-won claim and would let us ack
	// a K=1 write while a live co-owner already exists (the durability bug). We
	// union in the registry claims too so a starved-but-alive holder is still
	// reached. A no-barrier push makes the target claim + persist directly, so
	// K=2 is achieved deterministically without waiting for claim propagation.
	//
	// Total effort is bounded to 3× the liveness window, read here at loop entry
	// so a test that shrinks LivenessTimeout shrinks the bound with it (never
	// frozen into a constant). This bound is load-bearing, not belt-and-suspenders:
	// the handler ctx is the *service* lifetime, not per-request (the router hands
	// every command r.svr.Context()), so ctx.Done() never fires for a single write.
	// Meanwhile membership liveness rides multi-hop pubsub gossip (A learns B is
	// alive via a relay through C) while the barrier push needs a DIRECT A→B stream
	// dial — so "live per gossip yet undialable from here" (asymmetric partition,
	// NAT breakage) is a sustainable state that would otherwise pin this goroutine
	// spinning at BarrierRetryInterval forever, long after the client gave up. If a
	// co-owner stays unreachable past the liveness window, treat it as dead FOR THIS
	// WRITE: warn under-replicated and return — reconcile + CRDT catch-up
	// re-replicate later. A reachable co-owner acks on the first iteration, so the
	// bound is only ever reached under such a partition; strict durability is
	// unweakened in the common case.
	deadline := time.Now().Add(3 * hoarderSpecs.LivenessTimeout)
	reason := "no live co-owner" // why the barrier fell back to a local-only ack
	for {
		members := srv.activeMembers()
		target := targetReplicas(len(members))
		desired := placementDesired(hash, members, target)
		claims, _ := srv.listClaims(ctx, hash)

		// Push to every candidate holder (desired-by-membership ∪ registry claims):
		// a stale claim that is actually alive still acks; a genuinely dead one just
		// fails this attempt.
		for _, c := range dedup(append(append([]string{}, desired...), claims...)) {
			if c == self {
				continue
			}
			pid, err := peerCore.Decode(c)
			if err != nil {
				continue
			}
			resp, err := srv.kvStream.Send(hoarderSpecs.KVDBCommand, repl, pid)
			if err == nil && maps.TryString(resp, hoarderSpecs.BodyCode) == "" {
				return // a co-owner persisted it — K>=2 satisfied
			}
		}

		// Retry only while a LIVE co-owner still exists to receive it — desired is
		// live by construction and liveClaimants filters the registry to live
		// members, so a dead holder (which leaves membership within LivenessTimeout)
		// stops the loop instead of spinning on an unreachable peer.
		liveCoOwner := false
		for _, c := range dedup(append(append([]string{}, desired...), srv.liveClaimants(claims)...)) {
			if c != self {
				liveCoOwner = true
				break
			}
		}
		if !liveCoOwner {
			break // no live co-owner — genuinely under-replicated (reconcile re-replicates)
		}
		if time.Now().After(deadline) {
			// A co-owner is live per gossip but we still cannot dial it: stop
			// treating this write as blocked on an unreachable peer.
			reason = "co-owner live per gossip but undialable past the liveness window"
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(hoarderSpecs.BarrierRetryInterval):
		}
	}

	logger.Warnf("write to %s acked under-replicated: %s", hash, reason)
}

func (srv *Service) kvList(ctx context.Context, handle kvdb.KVDB, body command.Body) (cr.Response, error) {
	prefix := maps.TryString(body, hoarderSpecs.BodyPrefix)
	keys, err := handle.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("list failed with: %w", err)
	}
	return cr.Response{hoarderSpecs.BodyKeys: keys}, nil
}

func (srv *Service) kvListRegex(ctx context.Context, handle kvdb.KVDB, body command.Body) (cr.Response, error) {
	prefix := maps.TryString(body, hoarderSpecs.BodyPrefix)
	regexs, _ := maps.StringArray(body, hoarderSpecs.BodyRegexs)
	keys, err := handle.ListRegEx(ctx, prefix, regexs...)
	if err != nil {
		return nil, fmt.Errorf("listRegex failed with: %w", err)
	}
	return cr.Response{hoarderSpecs.BodyKeys: keys}, nil
}

// kvBatch applies a grouped list of put/delete ops through the kvdb's native
// batch — preserving grouped semantics rather than replaying individual puts.
func (srv *Service) kvBatch(ctx context.Context, handle kvdb.KVDB, hash string, body command.Body) (cr.Response, error) {
	rawOps, ok := body[hoarderSpecs.BodyOps].([]interface{})
	if !ok {
		return nil, fmt.Errorf("missing or malformed ops")
	}

	batch, err := handle.Batch(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening batch failed with: %w", err)
	}

	for _, ro := range rawOps {
		opMap := maps.SafeInterfaceToStringKeys(ro)
		kvop, err := maps.String(opMap, hoarderSpecs.BodyKVOp)
		if err != nil {
			return nil, fmt.Errorf("batch op missing kvop: %w", err)
		}
		key, err := maps.String(opMap, hoarderSpecs.BodyKey)
		if err != nil {
			return nil, fmt.Errorf("batch op missing key: %w", err)
		}
		switch kvop {
		case hoarderSpecs.KVPut:
			value, err := maps.ByteArray(opMap, hoarderSpecs.BodyValue)
			if err != nil {
				return nil, fmt.Errorf("batch put missing value: %w", err)
			}
			if err := srv.admitWrite(maps.TryString(body, hoarderSpecs.BodyProject), len(value)); err != nil {
				return cr.Response{hoarderSpecs.BodyCode: hoarderSpecs.CodeOverCapacity}, nil
			}
			enc, err := srv.cipherEncrypt(value)
			if err != nil {
				return nil, fmt.Errorf("encrypting batch value failed with: %w", err)
			}
			if err := batch.Put(key, enc); err != nil {
				return nil, err
			}
		case hoarderSpecs.KVDelete:
			if err := batch.Delete(key); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported batch op %q", kvop)
		}
	}

	if err := batch.Commit(); err != nil {
		return nil, fmt.Errorf("batch commit failed with: %w", err)
	}
	srv.replicateWrite(ctx, hash, body)
	return cr.Response{}, nil
}

func (srv *Service) kvSync(ctx context.Context, handle kvdb.KVDB, body command.Body) (cr.Response, error) {
	key := maps.TryString(body, hoarderSpecs.BodyKey)
	if err := handle.Sync(ctx, key); err != nil {
		return nil, fmt.Errorf("sync failed with: %w", err)
	}
	return cr.Response{}, nil
}
