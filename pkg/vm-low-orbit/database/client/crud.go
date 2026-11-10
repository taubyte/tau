package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/taubyte/go-sdk/errno"
	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	common "github.com/taubyte/tau/core/vm"
	"golang.org/x/sync/errgroup"
)

const (
	// maxBatchBytes bounds the keys and values a batch holds in host memory
	// until it is committed.
	maxBatchBytes = 32 << 20
	// maxOpenBatches bounds the batches of an instance, so the host memory they
	// hold: there is no abort, and an instance is pooled, so a batch left
	// uncommitted would otherwise live until its database closes.
	maxOpenBatches = 8
	// maxPatternLen bounds the regex of databaseListMatch.
	maxPatternLen = 1024
	// maxEntryReads is how many keys databaseListEntries reads at once: every
	// read is a network call, and one at a time costs a round trip per key.
	maxEntryReads = 16
)

// maxQueuedBatchBytes bounds what open batches hold on the node, across every
// instance: the per-instance bound alone lets many instances of a function
// queue maxOpenBatches × maxBatchBytes each. A var so tests can lower it.
var maxQueuedBatchBytes int64 = 256 << 20

// queuedBatchBytes is what open batches hold on the node right now.
var queuedBatchBytes atomic.Int64

// release gives back what b held to the node's budget. Called once, when b
// leaves f.batches.
func (b *batch) release() {
	queuedBatchBytes.Add(-int64(b.bytes))
}

// batch holds the writes a function queued for one commit. They stay on the
// host until databaseBatchCommit, so a batch never committed writes nothing.
type batch struct {
	db    *Database
	ops   []dbIface.Op
	bytes int
}

func (f *Factory) databaseBatchNew(ctx context.Context, module common.Module,
	databaseId,
	idPtr uint32,
) uint32 {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	f.databaseLock.Lock()
	if f.batches == nil {
		f.batches = make(map[uint32]*batch)
	}
	id := f.batchIdToGrab
	f.batchIdToGrab++
	if len(f.batches) >= maxOpenBatches {
		// Dropped rather than refused: abandoned batches must not stop this
		// instance from ever opening another. Ids only grow, so the lowest is
		// the oldest; a later use of it gets ErrorDatabaseNotFound.
		oldest := id
		for open := range f.batches {
			oldest = min(oldest, open)
		}
		f.batches[oldest].release()
		delete(f.batches, oldest)
	}
	f.batches[id] = &batch{db: database}
	f.databaseLock.Unlock()

	return uint32(f.WriteUint32Le(module, idPtr, id))
}

func (f *Factory) getBatch(batchId uint32) (*batch, errno.Error) {
	f.databaseLock.RLock()
	defer f.databaseLock.RUnlock()
	if b, ok := f.batches[batchId]; ok {
		return b, 0
	}

	return nil, errno.ErrorDatabaseNotFound
}

func (f *Factory) addBatchOp(batchId uint32, op dbIface.Op) errno.Error {
	f.databaseLock.Lock()
	defer f.databaseLock.Unlock()
	b, ok := f.batches[batchId]
	if !ok {
		return errno.ErrorDatabaseNotFound
	}
	n := len(op.Key) + len(op.Value)
	if b.bytes+n > maxBatchBytes {
		return errno.ErrorDatabasePutFailed
	}
	if queuedBatchBytes.Add(int64(n)) > maxQueuedBatchBytes {
		queuedBatchBytes.Add(-int64(n))
		return errno.ErrorDatabasePutFailed
	}
	b.bytes += n
	b.ops = append(b.ops, op)

	return 0
}

func (f *Factory) databaseBatchPut(ctx context.Context, module common.Module,
	batchId,
	keyPtr, keyLen,
	bufPtr, bufSize uint32,
) uint32 {
	if _, err := f.getBatch(batchId); err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	data, err := f.ReadBytes(module, bufPtr, bufSize)
	if err != 0 {
		return uint32(err)
	}

	// data is a view of guest memory, which the guest may reuse before commit.
	return uint32(f.addBatchOp(batchId, dbIface.Op{Key: key, Value: bytes.Clone(data)}))
}

func (f *Factory) databaseBatchDelete(ctx context.Context, module common.Module,
	batchId,
	keyPtr, keyLen uint32,
) uint32 {
	if _, err := f.getBatch(batchId); err != 0 {
		return uint32(err)
	}

	key, err := f.ReadString(module, keyPtr, keyLen)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.addBatchOp(batchId, dbIface.Op{Key: key, Delete: true}))
}

// databaseBatchCommit writes the batch and frees it, whether it succeeds or not.
func (f *Factory) databaseBatchCommit(ctx context.Context, module common.Module,
	batchId uint32,
) uint32 {
	f.databaseLock.Lock()
	b, ok := f.batches[batchId]
	delete(f.batches, batchId)
	f.databaseLock.Unlock()
	if !ok {
		return uint32(errno.ErrorDatabaseNotFound)
	}
	defer b.release()

	if _, err := f.getDatabase(b.db.Id); err != 0 {
		return uint32(err)
	}

	if err := b.db.KV().Batch(ctx, b.ops); err != nil {
		return uint32(errno.ErrorDatabasePutFailed)
	}

	return 0
}

// dropBatches frees the batches of e. Called under databaseLock.
func (f *Factory) dropBatches(e *Database) {
	for id, b := range f.batches {
		if b.db == e {
			b.release()
			delete(f.batches, id)
		}
	}
}

// listEntries reads every key under prefix with its value. A key deleted
// between the list and its read is left out.
func (f *Factory) listEntries(ctx context.Context, database *Database, prefix string) ([]byte, errno.Error) {
	keys, err0 := userKeys(ctx, database, prefix)
	if err0 != nil {
		return nil, errno.ErrorDatabaseListFailed
	}

	values := make([][]byte, len(keys))
	found := make([]bool, len(keys))
	var failed atomic.Uint32
	var reads errgroup.Group
	reads.SetLimit(maxEntryReads)
	for i, key := range keys {
		reads.Go(func() error {
			value, err := database.Get(ctx, key)
			switch {
			case err == errno.ErrorDatabaseKeyNotFound:
			case err != 0:
				failed.CompareAndSwap(0, uint32(err))
			default:
				values[i], found[i] = value, true
			}
			return nil
		})
	}
	reads.Wait()
	if rc := failed.Load(); rc != 0 {
		return nil, errno.Error(rc)
	}

	var encoded []byte
	for i, key := range keys {
		if found[i] {
			encoded = appendEntry(encoded, key, values[i])
		}
	}

	return encoded, 0
}

// appendEntry encodes one entry as: key, 0x00, value length (uint32 little
// endian), value. The key list encoding ends each key with 0x00 the same way.
// userKeys lists keys as the user stored them: the store lists them with a
// leading slash that Put, Get and Has accept without. The old List call keeps
// the slash; only the CRUD calls use this.
func userKeys(ctx context.Context, database *Database, prefix string) ([]string, error) {
	keys, err := database.KV().List(ctx, prefix)
	for i, k := range keys {
		keys[i] = strings.TrimPrefix(k, "/")
	}
	return keys, err
}

func appendEntry(encoded []byte, key string, value []byte) []byte {
	encoded = append(encoded, key...)
	encoded = append(encoded, 0)
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(len(value)))
	return append(encoded, value...)
}

func (f *Factory) databaseListEntries(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	dataPtr uint32,
) uint32 {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	prefix, err := f.ReadString(module, prefixPtr, prefixLen)
	if err != 0 {
		return uint32(err)
	}

	if rc, ok := f.WriteReported(module, entriesKey(databaseId, prefix), dataPtr); ok {
		return uint32(rc)
	}

	encoded, err := f.listEntries(ctx, database, prefix)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytesFor(module, entriesKey(databaseId, prefix), dataPtr, encoded))
}

func (f *Factory) databaseListEntriesSize(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	sizePtr uint32,
) uint32 {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	prefix, err := f.ReadString(module, prefixPtr, prefixLen)
	if err != 0 {
		return uint32(err)
	}

	encoded, err := f.listEntries(ctx, database, prefix)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytesSizeFor(module, entriesKey(databaseId, prefix), sizePtr, encoded))
}

// listMatch lists the keys under prefix matching pattern. A pattern too long
// or not valid is refused with ErrorDatabaseListFailed.
func (f *Factory) listMatch(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	patternPtr, patternLen uint32,
) (prefix, pattern string, keys []string, err errno.Error) {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return
	}

	if prefix, err = f.ReadString(module, prefixPtr, prefixLen); err != 0 {
		return
	}

	if patternLen > maxPatternLen {
		err = errno.ErrorDatabaseListFailed
		return
	}

	if pattern, err = f.ReadString(module, patternPtr, patternLen); err != 0 {
		return
	}

	if _, err0 := regexp.Compile(pattern); err0 != nil {
		err = errno.ErrorDatabaseListFailed
		return
	}

	all, err0 := userKeys(ctx, database, prefix)
	if err0 != nil {
		err = errno.ErrorDatabaseListFailed
		return
	}
	re := regexp.MustCompile(pattern) // validated above
	for _, k := range all {
		if re.MatchString(k) {
			keys = append(keys, k)
		}
	}

	return
}

func (f *Factory) databaseListMatch(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	patternPtr, patternLen,
	dataPtr uint32,
) uint32 {
	prefix, err := f.ReadString(module, prefixPtr, prefixLen)
	if err != 0 {
		return uint32(err)
	}
	pattern, err := f.ReadString(module, patternPtr, patternLen)
	if err != 0 {
		return uint32(err)
	}
	if rc, ok := f.WriteReported(module, matchKey(databaseId, prefix, pattern), dataPtr); ok {
		return uint32(rc)
	}

	_, _, keys, err := f.listMatch(ctx, module, databaseId, prefixPtr, prefixLen, patternPtr, patternLen)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceFor(module, matchKey(databaseId, prefix, pattern), dataPtr, keys))
}

func (f *Factory) databaseListMatchSize(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	patternPtr, patternLen,
	sizePtr uint32,
) uint32 {
	prefix, pattern, keys, err := f.listMatch(ctx, module, databaseId, prefixPtr, prefixLen, patternPtr, patternLen)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSizeFor(module, matchKey(databaseId, prefix, pattern), sizePtr, keys))
}

// listPage returns at most limit keys under prefix, in order, after cursor;
// an empty cursor starts at the first key.
// shortcut: lists every key under prefix for each page, move to a kvdb range
// query when prefixes hold many thousands of keys.
func (f *Factory) listPage(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	cursorPtr, cursorLen,
	limit uint32,
) (key string, page []string, err errno.Error) {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return
	}

	prefix, err := f.ReadString(module, prefixPtr, prefixLen)
	if err != 0 {
		return
	}

	cursor, err := f.ReadString(module, cursorPtr, cursorLen)
	if err != 0 {
		return
	}

	if limit == 0 {
		err = errno.ErrorZeroSize
		return
	}

	keys, err0 := userKeys(ctx, database, prefix)
	if err0 != nil {
		err = errno.ErrorDatabaseListFailed
		return
	}

	sort.Strings(keys)
	start := sort.SearchStrings(keys, cursor)
	if cursor != "" && start < len(keys) && keys[start] == cursor {
		start++
	}
	end := len(keys)
	if uint64(end-start) > uint64(limit) {
		end = start + int(limit)
	}

	return pageKey(databaseId, prefix, cursor, limit), keys[start:end], 0
}

func (f *Factory) databaseListPage(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	cursorPtr, cursorLen,
	limit,
	dataPtr uint32,
) uint32 {
	prefix, err := f.ReadString(module, prefixPtr, prefixLen)
	if err != 0 {
		return uint32(err)
	}
	cursor, err := f.ReadString(module, cursorPtr, cursorLen)
	if err != 0 {
		return uint32(err)
	}
	if rc, ok := f.WriteReported(module, pageKey(databaseId, prefix, cursor, limit), dataPtr); ok {
		return uint32(rc)
	}

	key, page, err := f.listPage(ctx, module, databaseId, prefixPtr, prefixLen, cursorPtr, cursorLen, limit)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceFor(module, key, dataPtr, page))
}

func (f *Factory) databaseListPageSize(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	cursorPtr, cursorLen,
	limit,
	sizePtr uint32,
) uint32 {
	key, page, err := f.listPage(ctx, module, databaseId, prefixPtr, prefixLen, cursorPtr, cursorLen, limit)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSliceSizeFor(module, key, sizePtr, page))
}

func (f *Factory) databaseCount(ctx context.Context, module common.Module,
	databaseId,
	prefixPtr, prefixLen,
	countPtr uint32,
) uint32 {
	database, err := f.getDatabase(databaseId)
	if err != 0 {
		return uint32(err)
	}

	prefix, err := f.ReadString(module, prefixPtr, prefixLen)
	if err != 0 {
		return uint32(err)
	}

	keys, err0 := userKeys(ctx, database, prefix)
	if err0 != nil {
		return uint32(errno.ErrorDatabaseListFailed)
	}

	return uint32(f.WriteUint32Le(module, countPtr, uint32(len(keys))))
}

func entriesKey(id uint32, prefix string) string {
	return fmt.Sprintf("database.entries/%d/%s", id, prefix)
}

func matchKey(id uint32, prefix, pattern string) string {
	return fmt.Sprintf("database.match/%d/%q/%s", id, pattern, prefix)
}

func pageKey(id uint32, prefix, cursor string, limit uint32) string {
	return fmt.Sprintf("database.page/%d/%d/%q/%s", id, limit, cursor, prefix)
}
