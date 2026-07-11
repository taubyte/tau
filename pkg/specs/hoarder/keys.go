package hoarder

// Registry key layout in the shared "hoarder" CRDT kvdb. Written on placement
// change only — never on liveness/last-used/load state (those ride the DHT or
// stay in-memory). See PLAN.md "Replica tracking".
//
// The replica target is system-calculated (min(DefaultReplicaTarget, liveFleet)),
// not stored per resource — so meta carries identity only, no Min/Max.
//
//	/hoarder/meta/<hash>                  cbor{Kind,ConfigId,ProjectId,ApplicationId,Match,Branch}
//	/hoarder/claims/<hash>/<peerID>       cbor{Since}
//	/hoarder/stash/meta/<cid>             cbor{Target,OwnerHash?}
//	/hoarder/stash/claims/<cid>/<peerID>  cbor{Since}
const (
	MetaPrefix        = "/hoarder/meta/"
	ClaimsPrefix      = "/hoarder/claims/"
	StashMetaPrefix   = "/hoarder/stash/meta/"
	StashClaimsPrefix = "/hoarder/stash/claims/"
)

// MetaKey is the durable placement record for a resource instance.
func MetaKey(hash string) string {
	return MetaPrefix + hash
}

// ClaimsPathOf is the prefix holding every claim for a resource instance.
func ClaimsPathOf(hash string) string {
	return ClaimsPrefix + hash + "/"
}

// ClaimKey names one holder's claim on a resource instance.
func ClaimKey(hash, peerID string) string {
	return ClaimsPathOf(hash) + peerID
}

// StashMetaKey is the durable placement record for a stashed CID.
func StashMetaKey(cid string) string {
	return StashMetaPrefix + cid
}

// StashClaimsPathOf is the prefix holding every pin-claim for a stashed CID.
func StashClaimsPathOf(cid string) string {
	return StashClaimsPrefix + cid + "/"
}

// StashClaimKey names one holder's pin-claim on a stashed CID.
func StashClaimKey(cid, peerID string) string {
	return StashClaimsPathOf(cid) + peerID
}
