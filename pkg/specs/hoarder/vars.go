package hoarder

import "time"

// MembersTopic carries hoarder heartbeats: the membership signal that drives
// deterministic (HRW) placement. Separate from the reconcile topic so a burst of
// resource changes can't starve liveness.
var MembersTopic = "/hoarder/v1.0/members"

// ReconcileTopic carries resource/claim change notifications (a bare instance
// hash). Receivers reconcile that hash immediately; a lost notification is fine
// because the periodic reconcile backstop re-checks everything.
var ReconcileTopic = "/hoarder/v1.0/reconcile"

// Tunables — exported so tests can shrink them. Defaults are production values.
var (
	// HeartbeatInterval is how often each hoarder announces itself on MembersTopic.
	HeartbeatInterval = 1 * time.Second

	// LivenessTimeout is how long since the last heartbeat before a member is
	// considered dead and dropped from the placement set. A few missed beats, so
	// a transient hiccup doesn't trigger a re-home. Death is confirmed by a direct
	// ping before drop (see membership.go).
	LivenessTimeout = 5 * time.Second

	// ReconcileBackstop is the period of the full re-reconcile safety net. The
	// fast path is event-driven (membership/resource/claim change); this only
	// catches missed notifications, so it can be lazy.
	ReconcileBackstop = 15 * time.Second

	// ReconcileJitter caps a small random delay a node adds before acting on a
	// re-home, so co-owners that react to the same death don't herd the registry.
	ReconcileJitter = 500 * time.Millisecond

	// BarrierRetryInterval is the backoff between K=2 barrier attempts. The barrier
	// retries a live co-claimant until the write lands (strict durability); it is
	// bounded by membership liveness, not a fixed timeout.
	BarrierRetryInterval = 200 * time.Millisecond

	// IdleTTL is how long a loaded instance may sit idle before it unloads.
	IdleTTL = 10 * time.Minute

	// DefaultReplicaTarget is the desired replica count for a database/storage
	// instance. The effective target is min(DefaultReplicaTarget, liveFleet),
	// so single-node clouds converge to 1 automatically.
	DefaultReplicaTarget = 3

	// DefaultStashReplicas is the target replica count for stashed CIDs when
	// the caller does not specify one.
	DefaultStashReplicas = 2

	// AssetSweepRetries bounds how many times the boot-time asset sweep retries
	// a failing TNS listing before giving up until the next boot.
	AssetSweepRetries = 10

	// AssetSweepRetryInterval is the backoff between asset-sweep TNS retries.
	AssetSweepRetryInterval = 6 * time.Second

	// AssetSweepFetchTimeout bounds fetching one asset's bytes during the sweep.
	AssetSweepFetchTimeout = 2 * time.Minute

	// AssetSweepInterval is the period of the recurring asset sweep after the
	// boot pass — the self-heal for an asset published while every push to the
	// stash failed (e.g. the fleet was unreachable during a build).
	AssetSweepInterval = 10 * time.Minute
)
