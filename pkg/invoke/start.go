package invoke

// start.go is the one place a workflow grant becomes the send that begins a
// run.
//
// # Why it is here and not in either caller
//
// A workflow runs on no tier — it IS queues — so starting one means sending to
// its first step's queue, and the grant an author wrote names the WORKFLOW. The
// composer resolves where a start lands and records it on that grant as an
// ordinary value; what neither the composer nor the author writes is the queue
// grant itself, so every firer has to build it.
//
// TWO CALLERS BUILT IT SEPARATELY AND ONE OF THEM DID NOT. pkg/invoke/wasm's
// `workflow.start` rebuilt the grant correctly, and services/worker's cron path
// passed the workflow's own — so a cron that started a workflow pushed an item
// into a queue named after the workflow, which no consumer ever opens. The item
// was accepted, the slot was claimed, and the run never happened: nothing
// errored anywhere. A convention with two implementations is a convention one
// of them will get wrong, so this is the implementation.

// The keys the composer writes from a workflow's `entry/start` record, and the
// two that say what it landed on. They are the vocabulary's own compiled paths.
const (
	SettingStartKind   = "entry/start/kind"
	SettingStartHandle = "entry/start/handle"

	// SettingSyncType is where the composer writes which primitive a sync grant
	// names — `queue` or `log`.
	SettingSyncType = "type"

	// SyncKind and QueueType are the kind and the type a start has to land on.
	SyncKind  = "sync"
	QueueType = "queue"

	// OpSend is the verb the produced grant carries, and the only one: what was
	// checked before this was called is the WORKFLOW grant, and this function
	// invents no permission beyond the send the entry record already describes.
	OpSend = "send"
)

// StartGrant is the send a `start` on this workflow grant spends.
//
// IT REPORTS false RATHER THAN GUESSING. A workflow whose entry record is
// missing, or whose entry names a log rather than a queue, is one a start has
// nowhere to land in — and THE TYPE IS CHECKED AND NOT ONLY THE KIND, because
// `sync` covers a queue and a log alike and a run started into a log would be a
// run with no delivery at all.
//
// THE PRODUCED GRANT CARRIES THE WORKFLOW'S SETTINGS, which is where the first
// step's own queue config already is: the composer merged it in when it
// resolved the entry, so a start and the dispatcher open one queue with one
// author's budget rather than two.
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
