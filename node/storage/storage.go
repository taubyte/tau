// Package storage backs `taubyte:storage/objects` on this node — the `storage`
// kind at `type: object`, served as the S3 object API.
//
// # Why it is its own package
//
// It is an ADAPTER: it sits between the wasm tier, which knows five methods and
// three sentinels, and two stores that know nothing about an object — a
// content-addressed one holding the bytes, and a replicated row holding the
// name. The composition root is the only place that may NAME all of those — but
// naming them and implementing the object API are different jobs, and conflating
// them is how node/ grew to eight thousand lines with twenty-two of these in one
// namespace.
//
// So the root hands this what it can ask: somewhere to put bytes, somewhere to
// put rows, and a clock. Everything this package needs is in its own signature,
// which is the property a type taking *Node does not have: that one can reach
// anything the node has, so nothing about it says what it actually depends on,
// and it cannot be built anywhere except beside the node.
//
// # Content-addressed underneath, named on top
//
// The BYTES go into the same store a build's artifacts do, so two objects with
// the same content are one object and neither the swarm nor the disk holds it
// twice. What this adds is a NAME: one row per object, in a replicated kvdb,
// keyed by the bucket's handle and the object's name.
//
// That split is why the two halves have different durability, and it is worth
// stating: the row replicates the way every other kvdb row does, and the bytes
// replicate the way every other content-addressed object does. An object whose
// row has reached a node and whose bytes have not is a `get` that waits on the
// DAG, which is the same shape a component that has not been fetched has.
//
// # A delete removes the name and not the bytes
//
// The bytes are content-addressed, so they may be another name's, another
// version's, or another project's. Collecting the ones nothing points at is a
// different job with a different safety argument — it has to know about every
// pointer in the cloud — and doing it from here would be a delete that can
// remove somebody else's object.
//
// # `versioning` is honoured by not collecting
//
// The vocabulary's `object/versioning` keeps historical versions. Since nothing
// collects, a replaced object's bytes stay in the store either way — so a
// bucket with versioning off is served STRONGER than it asked, which is the
// safe direction. What is missing is a way to ASK for an old version, and there
// is no verb for it: `get` takes a name and nothing else.
//
// # The size is enforced by scanning the index
//
// `size` is Required and it is a real ceiling here, not a measurement: a write
// that would take the bucket past it is refused with `full`, which is an arm of
// its own precisely so a caller can act on it. The scan is proportional to the
// number of objects in the bucket, which is the honest cost of not keeping a
// total — and a total is a counter, which is the one thing AGENTS.md says a
// kvdb key must never be: a value two nodes write differently.
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

// Rows is the index: one row per object, keyed by the bucket's handle and the
// object's name.
//
// FOUR METHODS AND NOT A kvdb, whose New also takes a logger and a rebroadcast
// interval — a node's decisions about a store, which a bucket holds no opinion
// about — and whose result has to be registered with the closers the root keeps.
//
// "NOT THERE" IS A bool AND NOT AN ERROR, because this is where a miss is
// decided: the tier's own ErrNotFound is what a guest must see for a name
// nobody wrote, and a backing that reported it as an error would make every
// miss indistinguishable from a store that could not be read — which is a
// retry loop around the thing a `get` does most.
//
// LIST ANSWERS WHOLE KEYS, prefix and all, in whatever shape the store
// normalises them into. See [bucket.name] for what that cost once.
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
	settingSize = "size"
	settingType = "type"
)

// Settings is every attribute path this adapter reads off a grant.
//
// IT IS EXPORTED SO THE ROOT CAN HOLD IT TO THE VOCABULARY. A renamed attribute
// does not fail here: the key is absent, the read is a zero value, and a
// declared ceiling silently becomes no ceiling at all. node/settings_test.go
// walks this list against the schema, and its own doc carries the post-mortem
// that test came out of.
var Settings = []string{settingSize, settingType}

// objectRow is what the index holds for one object.
//
// THE CID IS THE ONLY THING THAT COULD NOT BE RECOMPUTED. Size and modified
// could be read back off the bytes and the DAG, and they are here because
// `head` exists to answer without touching either.
//
// THE CID IS ALSO THE ETAG, which is what an etag is supposed to be: a token
// that changes exactly when the content does. S3 gives a weak one for anything
// multipart; a content id is strong for everything.
//
// METADATA IS A MAP AND NOT A LIST, because that is what it is — a header name
// appearing twice is one header — and because encoding/json sorts a map's keys,
// so two nodes writing the same object write the same bytes. That is the kvdb
// rule this package's doc states, and a list whose order came from the caller
// would break it.
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

// List is ListObjectsV2: a page of objects under a prefix, with a delimiter
// collapsing everything past it into common prefixes.
//
// # The token is the last key CONSUMED, not the last one answered
//
// A page that ends on a common prefix has read every object under it to produce
// that one line, and a token naming the prefix would resume before them — so
// the same objects would be walked again and the same prefix answered on every
// page after it. What is encoded is the last name this call looked at, so a
// resume skips exactly what was accounted for whether it was answered as an
// object or folded into a prefix.
//
// It is base64 because S3's is opaque and callers should not assemble one: a
// token this bucket did not issue is `bad-request`, not a silently different
// page.
//
// # Objects and prefixes count against max-keys together
//
// As S3 counts them. A page of 1000 folders is a page, and a caller that asked
// for 10 gets at most 10 things back however they were produced.
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

		// A NAME UNDER A PREFIX ALREADY ANSWERED IS CONSUMED BEFORE THE PAGE IS
		// CHECKED, and that ordering is the whole correctness of the token. A
		// page that ends on a common prefix has read every object under it to
		// produce that one line; stopping at the first of them would hand back
		// a cursor that resumes INSIDE the prefix, walk its siblings again and
		// answer the same prefix on the next page — forever, for a prefix with
		// more members than the page holds.
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

// What one row may hold beside the content id, in S3's own numbers.
//
// THESE ARE A CEILING ON A REPLICATED STORE AND NOT A STYLE RULE. The bucket's
// declared size bounds the BYTES, which go to the content-addressed store; the
// key, the content type and the user metadata go into a kvdb row that every
// node holding this deployment replicates, and nothing else bounds them. A
// guest writing a megabyte of metadata per object would fill every peer's
// datastore while the bucket reported itself empty.
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

// name is the object's own name, with this bucket's prefix off it.
//
// A kvdb List ANSWERS WITH A LEADING SLASH, because the store normalises every
// key through a datastore path — so trimming the prefix as this package spells
// it (no leading slash) trimmed nothing, `row` then looked the object up under
// the prefix twice over and missed, and a listing answered empty for a bucket
// that was full. `held` counted the same nothing, which made the declared size
// a ceiling on one object rather than on the bucket.
func (b *bucket) name(key string) string {
	return strings.TrimPrefix(strings.TrimPrefix(key, "/"), strings.TrimPrefix(b.prefix, "/"))
}

// ErrBadObjectName is a name that would not stay inside its bucket.
var ErrBadObjectName = errors.New("storage: an object name may not be empty or contain a path segment of . or ..")

// named is the key one object sits at, or an error when the name would leave
// this bucket.
//
// THE STORE PATH-CLEANS EVERY KEY, so `../../prod/QmOther/secret` appended to
// this bucket's prefix does not miss — it RESOLVES, into another deployment's
// bucket. A guest in staging could read, overwrite and delete production
// objects, with no error anywhere on the path.
//
// It refuses rather than cleaning, which is the same answer node/volume's
// `within` gives a checkpoint entry and for the same reason: nothing legitimate
// names a parent, so a name that does is not one of ours and is worth failing
// on rather than silently rewriting into some other object.
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
