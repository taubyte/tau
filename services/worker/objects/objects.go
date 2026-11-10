// Package objects backs `taubyte:storage/objects`, the `storage` kind at
// `type: object` as the S3 object API, over the storage service: a bucket is the
// storage resource in its deployment, an object a name in it, and each write a
// version the service holds on K nodes before it answers.
//
// The etag is the version's root CID, so the same content is the same etag. `size`
// is enforced against the bytes the storage service says the bucket holds — every
// version it keeps, under versioning every one it replaced — since a running total
// would be a contended counter; two racing writes can each pass it once.
package objects

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/storage"
)

// Instance is the one instance a bucket's objects live in.
const Instance = "objects"

// The setting keys services/worker writes, which are the vocabulary's own attribute
// paths.
const (
	settingSize       = specs.StorageKeySize
	settingType       = specs.StorageKeyType
	settingVersioning = specs.StorageObjectKeyVersioning
	settingExpire     = specs.StorageLifecycleKeyExpire
)

// Settings is every attribute path this adapter reads off a grant, exported so the
// instance's settings test can hold it to the vocabulary.
var Settings = []string{settingSize, settingType, settingVersioning, settingExpire}

// Buckets opens a grant's bucket on the storage service.
type Buckets struct{ store storage.Store }

var _ wasmtier.Buckets = (*Buckets)(nil)

// New is the adapter over store.
func New(store storage.Store) (*Buckets, error) {
	if store == nil {
		return nil, errors.New("objects: no storage service to keep objects in")
	}
	return &Buckets{store: store}, nil
}

// OpenBucket gives back the bucket a grant names.
func (b *Buckets) OpenBucket(_ context.Context, grant invoke.Grant) (wasmtier.Bucket, error) {
	project, environment, id := specs.SplitHandle(grant.Handle)
	at, err := storage.NewAddress(project, environment, id, Instance)
	if err != nil {
		return nil, fmt.Errorf("objects: %s: %w", grant.Handle, err)
	}
	return &bucket{
		store: b.store, at: at, size: grant.Number(settingSize),
		versioned: grant.Settings[settingVersioning] == "true", expire: time.Duration(grant.Number(settingExpire)),
	}, nil
}

// bucket is one object store.
type bucket struct {
	store storage.Store
	at    storage.Address

	// size is the ceiling the author declared, in bytes. Zero is no ceiling,
	// for the reason invoke.Grant.Number states.
	size int64

	// versioned is a bucket that keeps every version it replaces; any other has
	// the storage service collect them.
	versioned bool

	// expire is how long any version lives, the current one included; zero is
	// until it is superseded or deleted.
	expire time.Duration
}

var _ wasmtier.Bucket = (*bucket)(nil)

// ErrNoName is an object put with no name, which nothing could find again.
var ErrNoName = errors.New("objects: an object with no name cannot be found again")

func (b *bucket) Get(ctx context.Context, key string) (wasmtier.Object, error) {
	v, err := b.current(ctx, key)
	if err != nil {
		return wasmtier.Object{}, err
	}
	content, err := b.store.Read(ctx, v.Record.RootCID)
	if err != nil {
		// THE NAME IS HERE AND THE BYTES ARE NOT, which is not a miss: a miss
		// tells the caller to write it again over something on its way.
		return wasmtier.Object{}, fmt.Errorf("objects: reading %s: %w", key, err)
	}
	defer content.Close()

	body, err := io.ReadAll(content)
	if err != nil {
		return wasmtier.Object{}, err
	}
	return wasmtier.Object{Info: info(v), Body: body}, nil
}

func (b *bucket) Head(ctx context.Context, key string) (wasmtier.Info, error) {
	v, err := b.current(ctx, key)
	if err != nil {
		return wasmtier.Info{}, err
	}
	return info(v), nil
}

// Put stores the bytes as the name's next version and answers with its etag, so a
// caller that already knows the etag knows the write changed nothing.
func (b *bucket) Put(ctx context.Context, key string, value []byte, contentType string, metadata []wasmtier.Metadata) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: %w", wasmtier.ErrBadRequest, ErrNoName)
	}
	if err := withinRow(key, contentType, metadata); err != nil {
		return "", err
	}

	// THE CEILING IS WHAT THE BUCKET WOULD HOLD AFTER THIS WRITE: every version it
	// keeps counts, and in a bucket that keeps none what this name holds now is
	// taken off, so replacing 1MB with 1MB grows nothing there.
	if b.size > 0 {
		held, err := b.store.Size(ctx, b.at)
		if err != nil {
			return "", fmt.Errorf("objects: sizing %s: %w", b.at, err)
		}
		// A VERSIONED BUCKET COUNTS WHAT IT REPLACES: the version this write
		// supersedes is kept, and still held. Any other drops it.
		if previous, err := b.current(ctx, key); err == nil && !b.versioned {
			held -= min(held, previous.Record.Size)
		}
		if int64(held)+int64(len(value)) > b.size {
			return "", fmt.Errorf("%w: %s holds %d of %d bytes and this write is %d",
				wasmtier.ErrFull, key, held, b.size, len(value))
		}
	}

	o := storage.Object{ContentType: contentType, Metadata: metadataMap(metadata), Versioned: b.versioned, Expire: b.expire}
	v, err := b.store.Put(ctx, b.at, key, o, bytes.NewReader(value))
	if err != nil {
		return "", fmt.Errorf("objects: storing %s: %w", key, err)
	}
	return v.Record.RootCID, nil
}

func (b *bucket) Delete(ctx context.Context, key string) error {
	if _, err := b.current(ctx, key); err != nil {
		return err
	}
	_, err := b.store.Delete(ctx, b.at, key)
	return err
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
	versions, err := b.store.List(ctx, b.at, request.Prefix, after, 0)
	if err != nil {
		return wasmtier.Listing{}, err
	}

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
	for _, v := range versions {
		prefix, collapses := collapse(v.Name, request.Prefix, request.Delimiter)

		// A name under an already-answered prefix is consumed before the page is
		// checked, or the token would resume inside the prefix and repeat it.
		if collapses && folded[prefix] {
			last = v.Name
			continue
		}
		if answered == limit {
			listing.Truncated = true
			listing.NextToken = encodeContinuation(last)
			return listing, nil
		}
		if collapses {
			folded[prefix] = true
			listing.CommonPrefixes = append(listing.CommonPrefixes, prefix)
		} else {
			listing.Objects = append(listing.Objects, info(v))
		}
		answered++
		last = v.Name
	}
	return listing, nil
}

// current is name's live version, a miss as the tier's.
func (b *bucket) current(ctx context.Context, key string) (storage.Version, error) {
	v, err := b.store.Current(ctx, b.at, key)
	if errors.Is(err, storage.ErrNotFound) {
		return storage.Version{}, wasmtier.ErrNotFound
	}
	return v, err
}

// held is what this bucket holds now, summed over its live versions.
// info is one version as the tier's answer, metadata sorted so two reads of one
// object say the same thing.
func info(v storage.Version) wasmtier.Info {
	out := wasmtier.Info{
		Key:         v.Name,
		ETag:        v.Record.RootCID,
		Size:        int64(v.Record.Size),
		Modified:    int64(v.Record.CreatedMS / 1000),
		ContentType: v.Record.ContentType,
	}
	if len(v.Record.Metadata) == 0 {
		return out
	}
	names := make([]string, 0, len(v.Record.Metadata))
	for name := range v.Record.Metadata {
		names = append(names, name)
	}
	sort.Strings(names)
	out.Metadata = make([]wasmtier.Metadata, 0, len(names))
	for _, name := range names {
		out.Metadata = append(out.Metadata, wasmtier.Metadata{Name: name, Value: v.Record.Metadata[name]})
	}
	return out
}

// metadataMap folds the tier's pairs into a map. A name given twice keeps the LAST
// value, which is what a header map does everywhere.
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

// maxObjectKeys is both the default page and the ceiling on a larger ask: S3's own
// number, which every client's paging loop is written around.
const maxObjectKeys = 1000

// What one version row may hold beside its root, in S3's numbers: these bound the
// replicated row every holder of the shard keeps.
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
var ErrBadContinuation = errors.New("objects: that is not a continuation token this bucket issued")

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
