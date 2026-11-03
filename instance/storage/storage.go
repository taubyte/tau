// Package storage backs `taubyte:storage/objects`, the `storage` kind at
// `type: object` as the S3 object API: an adapter over a content-addressed store for
// bytes and a replicated row for each name.
//
// Same content is one object; the row and the bytes replicate separately, so a row
// can arrive before its bytes. A delete removes the name, not the bytes, which may be
// another name's. Nothing collects, so `object/versioning` off is served stronger than
// asked; there is no verb to read an old version. `size` is enforced by scanning the
// index, since a running total would be a contended counter.
package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// Assets is the content-addressed store an object's bytes go into.
//
// TWO METHODS, WHICH IS ALL AN OBJECT IS DOWN HERE: put bytes, get them back.
// The id is a function of the content, which is why two names holding one
// content are one object and why the id is also the etag.
type Assets interface {
	Put(ctx context.Context, content io.Reader) (string, error)
	Get(ctx context.Context, id string) (io.ReadSeekCloser, error)
}

// Rows is the index: one row per object, keyed by the bucket's handle and the name.
// "Not there" is a bool, so a miss is never confused with an unreadable store. List
// answers whole keys as the store normalises them (see bucket.name).
type Rows interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Stores opens the one database every bucket on this node shares.
//
// ONE METHOD, ASKED LAZILY, so a node that serves no bucket opens no database —
// and a node whose operator gave it no store at all refuses by name the first
// time a workload actually reaches one.
type Stores interface {
	Store() (Rows, error)
}

// Buckets is the adapter: one index for every bucket this node serves, with the
// handle as the key prefix. Construct it with New.
type Buckets struct {
	assets Assets
	stores Stores
	now    func() time.Time

	mu   sync.Mutex
	rows Rows
}

var _ wasmtier.Buckets = (*Buckets)(nil)

// ErrNoObjectStore is a node asked for storage with no asset store. It is a
// composition fault: the bytes are content-addressed and only whoever holds the
// machine's DAG has somewhere to put them.
var ErrNoObjectStore = errors.New("storage: an object keeps its bytes in the asset store, and this node has none")

// New wires the adapter.
//
// now MAY BE NIL and is this node's clock when it is not: what an object says it
// was modified at is read off it, so a test that moves time moves what a `head`
// answers. Nil is the ordinary machine, where the clock is the machine's.
//
// assets and stores may not be nil. Without bytes there is no object, and
// without rows there is no name to find it by — and the root has something to
// hand over even on a node whose operator gave it no database factory: that
// store refuses, by name, the first time a workload opens a bucket.
func New(assets Assets, stores Stores, now func() time.Time) (*Buckets, error) {
	if assets == nil {
		return nil, ErrNoObjectStore
	}
	if stores == nil {
		return nil, errors.New("storage: no index to keep an object's name in")
	}
	return &Buckets{assets: assets, stores: stores, now: now}, nil
}

// The setting keys services/worker writes, which are the vocabulary's own
// attribute paths.
const (
	settingSize = specs.StorageKeySize
	settingType = specs.StorageKeyType
)

// Settings is every attribute path this adapter reads off a grant, exported so
// instance/settings_test.go can hold it to the vocabulary.
var Settings = []string{settingSize, settingType}

// objectRow is what the index holds for one object. The CID is the etag, strong for
// everything; size and modified let `head` answer without the bytes. Metadata is a map
// so encoding/json sorts it and two nodes write the same bytes.
type objectRow struct {
	CID         string            `json:"cid"`
	Size        int64             `json:"size"`
	Modified    int64             `json:"modified"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// info is one row as the tier's answer, with the metadata sorted so two reads
// of one object say the same thing.
func (r objectRow) info(key string) wasmtier.Info {
	out := wasmtier.Info{
		Key:         key,
		ETag:        r.CID,
		Size:        r.Size,
		Modified:    r.Modified,
		ContentType: r.ContentType,
	}
	if len(r.Metadata) == 0 {
		return out
	}

	names := make([]string, 0, len(r.Metadata))
	for name := range r.Metadata {
		names = append(names, name)
	}
	sort.Strings(names)

	out.Metadata = make([]wasmtier.Metadata, 0, len(names))
	for _, name := range names {
		out.Metadata = append(out.Metadata, wasmtier.Metadata{Name: name, Value: r.Metadata[name]})
	}
	return out
}

// OpenBucket gives back the bucket a grant names.
func (b *Buckets) OpenBucket(_ context.Context, grant invoke.Grant) (wasmtier.Bucket, error) {
	rows, err := b.store()
	if err != nil {
		return nil, err
	}

	return &bucket{
		rows:   rows,
		assets: b.assets,
		// THE PREFIX ENDS IN "/" so a bucket called QmA cannot see QmAB's,
		// which is the rule pkg/cache states and keeps for the same reason.
		prefix: "objects/" + grant.Handle + "/",
		size:   grant.Number(settingSize),
		now:    b.now,
	}, nil
}

// store asks for the one index, once. It runs under its own lock so two
// concurrent first-opens cannot open two.
func (b *Buckets) store() (Rows, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.rows != nil {
		return b.rows, nil
	}
	rows, err := b.stores.Store()
	if err != nil {
		return nil, err
	}
	b.rows = rows
	return rows, nil
}

// Close releases nothing of its own: the index is closed with the node and the
// bytes are the store's.
func (b *Buckets) Close() error { return nil }

// bucket is one object store.
type bucket struct {
	rows   Rows
	assets Assets
	prefix string

	// size is the ceiling the author declared, in bytes. Zero means the
	// compiled resource carried none, which the vocabulary does not allow —
	// and an absent one is no ceiling rather than a refusal, for the reason
	// invoke.Grant.Number states.
	size int64

	now func() time.Time
}

var _ wasmtier.Bucket = (*bucket)(nil)

func (b *bucket) Get(ctx context.Context, key string) (wasmtier.Object, error) {
	row, err := b.row(ctx, key)
	if err != nil {
		return wasmtier.Object{}, err
	}

	content, err := b.assets.Get(ctx, row.CID)
	if err != nil {
		// THE NAME IS HERE AND THE BYTES ARE NOT. That is a fetch that has not
		// happened rather than an object that does not exist, so it is not a
		// miss: a miss would tell the caller to write it again, which would
		// mean writing over something that is on its way.
		return wasmtier.Object{}, fmt.Errorf("storage: reading %s: %w", row.CID, err)
	}
	defer content.Close()

	body, err := io.ReadAll(content)
	if err != nil {
		return wasmtier.Object{}, err
	}
	return wasmtier.Object{Info: row.info(key), Body: body}, nil
}

func (b *bucket) Head(ctx context.Context, key string) (wasmtier.Info, error) {
	row, err := b.row(ctx, key)
	if err != nil {
		return wasmtier.Info{}, err
	}
	return row.info(key), nil
}

// Put stores the bytes and the name, and answers with the etag it stored under
// — which is the content id, so a caller that already knows the etag knows the
// write changed nothing.
func (b *bucket) Put(ctx context.Context, key string, value []byte, contentType string, metadata []wasmtier.Metadata) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: an object with no name cannot be found again", ErrBadObjectName)
	}
	if err := withinRow(key, contentType, metadata); err != nil {
		return "", err
	}
	at, err := b.named(key)
	if err != nil {
		return "", err
	}

	// THE CEILING IS CHECKED BEFORE THE BYTES GO IN, and what is checked is
	// what the bucket would hold AFTER this write — with whatever this name
	// already holds taken off, because replacing a 1MB object with a 1MB
	// object does not grow anything.
	if b.size > 0 {
		held, err := b.held(ctx)
		if err != nil {
			return "", err
		}
		if previous, err := b.row(ctx, key); err == nil {
			held -= previous.Size
		}
		if held+int64(len(value)) > b.size {
			return "", fmt.Errorf("%w: %s holds %d of %d bytes and this write is %d",
				wasmtier.ErrFull, key, held, b.size, len(value))
		}
	}

	id, err := b.assets.Put(ctx, strings.NewReader(string(value)))
	if err != nil {
		return "", fmt.Errorf("storage: storing %s: %w", key, err)
	}

	row, err := json.Marshal(objectRow{
		CID:         id,
		Size:        int64(len(value)),
		Modified:    b.clock().Unix(),
		ContentType: contentType,
		Metadata:    metadataMap(metadata),
	})
	if err != nil {
		return "", err
	}
	if err := b.rows.Put(ctx, at, row); err != nil {
		return "", err
	}
	return id, nil
}

// metadataMap folds the tier's pairs into what the row holds. A name given
// twice keeps the LAST value, which is what a header map does everywhere.
func metadataMap(metadata []wasmtier.Metadata) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]string, len(metadata))
	for _, entry := range metadata {
		out[entry.Name] = entry.Value
	}
	return out
}

func (b *bucket) Delete(ctx context.Context, key string) error {
	if _, err := b.row(ctx, key); err != nil {
		return err
	}
	at, err := b.named(key)
	if err != nil {
		return err
	}
	// THE ROW AND NOT THE BYTES — see the package doc.
	return b.rows.Delete(ctx, at)
}

// List is ListObjectsV2: a page under a prefix, a delimiter folding names into common
// prefixes. The token (opaque base64) is the last name consumed, so a page ending on
// a common prefix resumes past all its members. Objects and prefixes count together
// against max-keys, as S3 counts them.
func (b *bucket) List(ctx context.Context, request wasmtier.ListRequest) (wasmtier.Listing, error) {
	after, err := decodeContinuation(request.Token)
	if err != nil {
		return wasmtier.Listing{}, err
	}

	at, err := b.named(request.Prefix)
	if err != nil {
		return wasmtier.Listing{}, err
	}

	keys, err := b.rows.List(ctx, at)
	if err != nil {
		return wasmtier.Listing{}, err
	}

	names := make([]string, 0, len(keys))
	for _, key := range keys {
		names = append(names, b.name(key))
	}
	// SORTED HERE rather than trusted: the answer's order is this call's
	// promise, and a continuation token is only meaningful over a stable one.
	sort.Strings(names)

	limit := int(request.MaxKeys)
	if limit <= 0 || limit > maxObjectKeys {
		limit = maxObjectKeys
	}

	var (
		listing  wasmtier.Listing
		answered int
		last     string
		folded   = map[string]bool{}
	)
	for _, name := range names {
		if after != "" && name <= after {
			continue
		}

		prefix, collapses := collapse(name, request.Prefix, request.Delimiter)

		// A name under an already-answered prefix is consumed before the page is checked, or
		// the token would resume inside the prefix and repeat it forever.
		if collapses && folded[prefix] {
			last = name
			continue
		}

		if answered == limit {
			// SOMETHING IS LEFT. The token is the last name consumed, which is
			// the one before this.
			listing.Truncated = true
			listing.NextToken = encodeContinuation(last)
			return listing, nil
		}

		if collapses {
			folded[prefix] = true
			listing.CommonPrefixes = append(listing.CommonPrefixes, prefix)
			answered++
			last = name
			continue
		}

		row, err := b.row(ctx, name)
		if err != nil {
			// A row that vanished between the listing and the read. It is not
			// this call's business to report somebody else's delete — but it
			// IS consumed, or a resume would stop on it forever.
			last = name
			continue
		}
		last = name
		listing.Objects = append(listing.Objects, row.info(name))
		answered++
	}
	return listing, nil
}

// maxObjectKeys is both the default page and the ceiling on a larger ask. It is
// S3's own number, which is what every client's paging loop is written around.
const maxObjectKeys = 1000

// What one row may hold beside the content id, in S3's numbers. The size bounds only
// the bytes; these bound the replicated row every peer holds.
const (
	maxObjectKeyBytes    = 1024
	maxObjectTypeBytes   = 256
	maxObjectMetaBytes   = 2048
	maxObjectMetaEntries = 32
)

// withinRow refuses what would not fit in one index row.
func withinRow(key, contentType string, metadata []wasmtier.Metadata) error {
	switch {
	case len(key) > maxObjectKeyBytes:
		return fmt.Errorf("%w: a key is at most %d bytes and this one is %d",
			wasmtier.ErrBadRequest, maxObjectKeyBytes, len(key))
	case len(contentType) > maxObjectTypeBytes:
		return fmt.Errorf("%w: a content type is at most %d bytes and this one is %d",
			wasmtier.ErrBadRequest, maxObjectTypeBytes, len(contentType))
	case len(metadata) > maxObjectMetaEntries:
		return fmt.Errorf("%w: an object carries at most %d metadata entries and this one has %d",
			wasmtier.ErrBadRequest, maxObjectMetaEntries, len(metadata))
	}

	var held int
	for _, entry := range metadata {
		held += len(entry.Name) + len(entry.Value)
	}
	if held > maxObjectMetaBytes {
		return fmt.Errorf("%w: user metadata is at most %d bytes and this is %d",
			wasmtier.ErrBadRequest, maxObjectMetaBytes, held)
	}
	return nil
}

// collapse is whether a name folds into a common prefix, and which.
func collapse(name, prefix, delimiter string) (string, bool) {
	if delimiter == "" {
		return "", false
	}
	rest := strings.TrimPrefix(name, prefix)
	cut := strings.Index(rest, delimiter)
	if cut < 0 {
		return "", false
	}
	return prefix + rest[:cut+len(delimiter)], true
}

// ErrBadContinuation is a token this bucket did not issue.
var ErrBadContinuation = errors.New("storage: that is not a continuation token this bucket issued")

func encodeContinuation(after string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(after))
}

func decodeContinuation(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	after, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("%w: %w", wasmtier.ErrBadRequest, ErrBadContinuation)
	}
	return string(after), nil
}

// held is what this bucket holds now, by scanning its index. See the package doc
// for why it is a scan rather than a counter.
func (b *bucket) held(ctx context.Context) (int64, error) {
	keys, err := b.rows.List(ctx, b.prefix)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, key := range keys {
		row, err := b.row(ctx, b.name(key))
		if err != nil {
			continue
		}
		total += row.Size
	}
	return total, nil
}

// row reads one object's index entry.
func (b *bucket) row(ctx context.Context, key string) (objectRow, error) {
	at, err := b.named(key)
	if err != nil {
		return objectRow{}, err
	}

	value, found, err := b.rows.Get(ctx, at)
	if err != nil {
		return objectRow{}, err
	}
	if !found {
		return objectRow{}, wasmtier.ErrNotFound
	}

	var row objectRow
	if err := json.Unmarshal(value, &row); err != nil {
		return objectRow{}, fmt.Errorf("storage: the index row for %s is not one: %w", key, err)
	}
	return row, nil
}

// name is the object's name with this bucket's prefix off. A kvdb List answers with a
// leading slash, so the prefix is trimmed as the store spells it.
func (b *bucket) name(key string) string {
	return strings.TrimPrefix(strings.TrimPrefix(key, "/"), strings.TrimPrefix(b.prefix, "/"))
}

// ErrBadObjectName is a name that would not stay inside its bucket.
var ErrBadObjectName = errors.New("storage: an object name may not be empty or contain a path segment of . or ..")

// named is the key one object sits at, refusing a name that would leave the bucket:
// the store path-cleans keys, so `../../prod/QmOther/secret` would resolve into
// another deployment's bucket.
func (b *bucket) named(key string) (string, error) {
	if err := insideBucket(key); err != nil {
		return "", err
	}
	return b.prefix + key, nil
}

// insideBucket refuses a guest-supplied name that would not stay in its bucket.
// An empty prefix is legal — it is every object — so this is shared by the
// lookups and by the listing.
func insideBucket(name string) error {
	for _, segment := range strings.Split(name, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%w: %q", ErrBadObjectName, name)
		}
	}
	return nil
}

func (b *bucket) clock() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}
