// Package migration moves node-local project data — per-instance kvdbs under
// /crdt/<hash> and locally-held storage file bytes — into hoarder-hosted
// instances and the hoarder stash, then scrubs the local copies. It runs at
// substrate boot (bounded, then background) and is idempotent: every pass
// re-enumerates what is still local, replays it conditionally (putnx — a value
// written through the live path is never overwritten), verifies by read-back,
// and only then deletes. Source data is never deleted before verified
// read-back; unresolvable namespaces are left intact and reported.
package migration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
)

var logger = log.Logger("tau.substrate.migration")

// Tunables — exported so tests can shrink them.
var (
	// BootTimeout bounds the synchronous boot-time pass; work left when it
	// expires continues in the background (the node serves meanwhile).
	BootTimeout = 10 * time.Minute

	// DrainInterval is the background retry period while local data remains —
	// it picks up instances that become resolvable later (a hoarder meta
	// appears when live traffic first-touches a regex-matched instance) and
	// retries transient failures.
	DrainInterval = 30 * time.Second

	// ReplayWorkers is the per-instance bound on in-flight key replays.
	ReplayWorkers = 8
)

// Migrator owns the migration lifecycle for one substrate node.
type Migrator struct {
	node    peer.Node
	hoarder hoarderIface.Client
	tns     tnsIface.Client

	passMu sync.Mutex // one pass at a time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	drainMu sync.Mutex
	drainOn bool
}

// New builds a Migrator. ctx is the service lifetime: Close cancels its child
// and joins any background work before returning.
func New(ctx context.Context, node peer.Node, hoarder hoarderIface.Client, tns tnsIface.Client) *Migrator {
	m := &Migrator{node: node, hoarder: hoarder, tns: tns}
	m.ctx, m.cancel = context.WithCancel(ctx)
	return m
}

// Boot runs the synchronous boot-time pass, bounded by BootTimeout. A node
// with no local project data pays one prefix query. Work left at the bound —
// or instances that need live traffic to become resolvable — continues on the
// background drain.
func (m *Migrator) Boot() *Report {
	hashes, err := m.namespaces(m.ctx)
	if err != nil {
		logger.Errorf("enumerating local data failed with: %s", err)
		return &Report{}
	}
	if len(hashes) == 0 {
		return &Report{}
	}

	logger.Infof("found %d node-local data namespace(s), migrating to hoarders", len(hashes))
	bctx, cancel := context.WithTimeout(m.ctx, BootTimeout)
	defer cancel()

	report := m.Migrate(bctx)
	if report.RemainingCount() > 0 {
		logger.Errorf("data migration incomplete: %s — continuing in background", report.Summary())
		m.armDrain()
	} else {
		logger.Infof("data migration complete: %s", report.Summary())
	}
	return report
}

// Migrate runs one full pass: enumerate → resolve → replay → verify → scrub →
// sweep. Safe to re-run at any time; passes serialize. Nothing local is
// deleted unless its instance verified read-back through the hoarder path and
// (for storage bytes) the stash reached its replica target.
func (m *Migrator) Migrate(ctx context.Context) *Report {
	m.passMu.Lock()
	defer m.passMu.Unlock()

	report := &Report{Instances: make(map[string]*InstanceReport)}

	hashes, err := m.namespaces(ctx)
	if err != nil {
		report.Err = fmt.Sprintf("enumerate: %s", err)
		return report
	}
	if len(hashes) == 0 {
		return report
	}

	resolved, unresolved, err := m.resolve(ctx, hashes)
	if err != nil {
		// Resolution is all-or-nothing per source: a failed TNS/hoarder listing
		// carries every namespace over to the next pass — never classified,
		// never touched.
		report.Err = fmt.Sprintf("resolve: %s", err)
		report.Unresolved = hashes
		return report
	}
	report.Unresolved = unresolved

	order := make([]string, 0, len(resolved))
	for h := range resolved {
		order = append(order, h)
	}
	sort.Strings(order)

	for _, hash := range order {
		if ctx.Err() != nil {
			report.Err = ctx.Err().Error()
			break
		}
		report.Instances[hash] = m.replayInstance(ctx, resolved[hash])
	}

	m.scrubAndSweep(ctx, report, hashes)
	return report
}

// armDrain starts (once) the background loop that re-runs Migrate until
// nothing local remains.
func (m *Migrator) armDrain() {
	m.drainMu.Lock()
	defer m.drainMu.Unlock()
	if m.drainOn {
		return
	}
	m.drainOn = true

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.drainMu.Lock()
			m.drainOn = false
			m.drainMu.Unlock()
		}()
		ticker := time.NewTicker(DrainInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				report := m.Migrate(m.ctx)
				if report.Err == "" && report.RemainingCount() == 0 {
					logger.Info("data migration drained: nothing local remains")
					return
				}
				logger.Infof("data migration drain: %s", report.Summary())
			}
		}
	}()
}

// Close stops background work and joins it — a canceled drain must not touch
// the node's store or the clients while the service tears them down.
func (m *Migrator) Close() {
	m.cancel()
	m.wg.Wait()
}

// splitNamespaceKey returns the namespace hash of a raw /crdt/... datastore key.
func splitNamespaceKey(key string) string {
	rest := strings.TrimPrefix(key, crdtPrefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}
