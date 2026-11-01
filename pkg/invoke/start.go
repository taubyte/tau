package invoke

import "github.com/taubyte/tau/pkg/specs"

// start.go is the one place a workflow grant becomes the send that begins a run. A
// workflow is queues, so a start sends to its first step's queue; the composer
// records where on the workflow grant, and every firer (the wasm `workflow.start`,
// the worker's cron) builds the queue grant here so none gets it wrong.

// The keys the composer writes from a workflow's `entry/start` record, and the
// two that say what it landed on. They are the vocabulary's own compiled paths.
const (
	SettingStartKind   = "entry/start/kind"
	SettingStartHandle = "entry/start/handle"

	// SettingSyncType is where the composer writes which primitive a sync grant
	// names — `queue` or `log`.
	SettingSyncType = specs.SyncKeyType

	// SyncKind and QueueType are the kind and the type a start has to land on.
	SyncKind  = specs.KindSync
	QueueType = specs.SyncTypeQueue

	// OpSend is the produced grant's only verb, inventing no permission beyond the
	// entry record's send.
	OpSend = "send"
)

// StartGrant is the send a `start` on this workflow grant spends. It reports false
// when the entry record is missing or names a log, not a queue. The produced grant
// carries the workflow's settings, where the composer merged the first step's
// queue config.
func StartGrant(grant Grant) (Grant, bool) {
	handle := grant.Settings[SettingStartHandle]
	if handle == "" ||
		grant.Settings[SettingStartKind] != SyncKind ||
		grant.Settings[SettingSyncType] != QueueType {
		return Grant{}, false
	}
	return Grant{
		Kind:     SyncKind,
		Label:    grant.Label,
		Handle:   handle,
		Ops:      []string{OpSend},
		Settings: grant.Settings,
	}, true
}
