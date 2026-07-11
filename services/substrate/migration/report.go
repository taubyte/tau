package migration

import "fmt"

// InstanceReport is the per-instance outcome of one pass.
type InstanceReport struct {
	Match string
	Kind  string

	Written    int // keys replayed (absent remotely)
	Existed    int // keys already present remotely with the same value
	Superseded int // keys present remotely with a different value (live write wins)

	FilesStashed      int // locally-held file bytes pushed to the stash this pass
	FilesAwaitingRepl int // locally-held file bytes below the stash replica target
	FilesElsewhere    int // file bytes this node never held (another holder pushes)

	Verified bool // every local key read back through the hoarder path
	Scrubbed bool // local namespace deleted (implies Verified + bytes at target)

	Err string

	// fileCids are this instance's file content CIDs whose bytes THIS node
	// holds — the set gated on stash replication before local deletion.
	fileCids []string
}

// Report is the outcome of one Migrate pass.
type Report struct {
	Instances   map[string]*InstanceReport
	Unresolved  []string // namespaces with no identity yet — left intact, retried
	SweptKeys   int      // raw namespace keys deleted
	SweptBlocks int      // blockstore blocks deleted
	Err         string
}

// RemainingCount is how many local namespaces still hold data after the pass.
func (r *Report) RemainingCount() int {
	n := len(r.Unresolved)
	for _, ir := range r.Instances {
		if !ir.Scrubbed {
			n++
		}
	}
	return n
}

// Empty reports a pass that found nothing to do.
func (r *Report) Empty() bool {
	return len(r.Instances) == 0 && len(r.Unresolved) == 0 && r.Err == ""
}

// Summary is a one-line operator-facing digest.
func (r *Report) Summary() string {
	var written, existed, superseded, stashed, scrubbed, failed int
	for _, ir := range r.Instances {
		written += ir.Written
		existed += ir.Existed
		superseded += ir.Superseded
		stashed += ir.FilesStashed
		if ir.Scrubbed {
			scrubbed++
		}
		if ir.Err != "" {
			failed++
		}
	}
	s := fmt.Sprintf("%d instance(s): %d scrubbed, %d failed, %d unresolved; keys %d written / %d existing / %d superseded; %d file(s) stashed; swept %d keys, %d blocks",
		len(r.Instances), scrubbed, failed, len(r.Unresolved), written, existed, superseded, stashed, r.SweptKeys, r.SweptBlocks)
	if r.Err != "" {
		s += "; error: " + r.Err
	}
	return s
}
