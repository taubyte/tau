package service

import "sync"

// buildSlots counts the builds handed out per project and not yet finished.
// shortcut: leader memory only, a restart forgets running builds; upgrade by rebuilding from /assigned.
type buildSlots struct {
	mu    sync.Mutex
	byJob map[string]string // job id -> project id
}

// take records jid as running for project unless the project already runs cap builds.
func (b *buildSlots) take(jid, project string, cap int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byJob == nil {
		b.byJob = map[string]string{}
	}
	if _, ok := b.byJob[jid]; ok {
		return true
	}
	n := 0
	for _, p := range b.byJob {
		if p == project {
			n++
		}
	}
	if cap > 0 && n >= cap {
		return false
	}
	b.byJob[jid] = project
	return true
}

func (b *buildSlots) release(jid string) {
	b.mu.Lock()
	delete(b.byJob, jid)
	b.mu.Unlock()
}
