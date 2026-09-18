package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// storage.go is the host half of `taubyte:storage/objects` — the `storage` kind
// at `type: object`.
//
// # It is the S3 object API
//
// Get, Head, Put, Delete, ListObjectsV2, with an etag, a content type, user
// metadata, a delimiter that collapses prefixes and a continuation token. Every
// client an author already has is written against those; a shape of our own
// would be a shape all of them need an adapter for.
//
// See wit/deps/storage/storage.wit for what is deliberately deferred — range
// reads and multipart, both of which want a streaming body rather than another
// `list<u8>`.
//
// # It is not kv.go with a third name
//
// The verbs overlap and the thing does not: a kv database holds values a guest
// wrote and this holds OBJECTS, which have a size, a type and a modification
// time worth asking about without reading them, and which are content-addressed
// underneath so two identical ones are one. `head` is what that difference
// buys, and a shared interface would have nowhere to put it.

const storageInterface = "taubyte:storage/objects@0.1.0"

// The vocabulary's verbs for the storage kind.
const (
	opObjectGet    = "get"
	opObjectHead   = "head"
	opObjectPut    = "put"
	opObjectDelete = "delete"
	opObjectList   = "list"
)

// The error variant's cases, in the order the WIT declares them.
const (
	errNoSuchStorage uint32 = iota
	errStorageDenied
	errStorageNotFound
	errStorageFull
	errStorageBadRequest
	errStorageUnavailable
)

var (
	// ErrNoStorage is a spec that grants storage on a tier composed without a
	// backing.
	ErrNoStorage = errors.New("wasm: this resource reaches storage and this tier has nothing to open a bucket with")

	// ErrFull is a bucket at the size its author declared. It is its own arm
	// because a caller acts on it — write less, delete something — where an
	// `unavailable` reads as an outage.
	ErrFull = errors.New("wasm: the bucket is at its declared size")

	// ErrBadRequest is a request that does not make sense: a continuation token
	// this bucket never issued, a key with a path segment in it. S3's 400, and
	// its own arm because a caller FIXES it rather than retrying it.
	//
	// It is not document.go's ErrMalformed, which is about the SHAPE OF A VALUE
	// rather than of a request — a caller fixes each one somewhere else.
	ErrBadRequest = errors.New("wasm: that request cannot be served as written")
)

// Metadata is one entry of an object's user metadata — S3's `x-amz-meta-*`
// without the prefix, which belongs to its REST binding rather than to the
// object.
type Metadata struct {
	Name  string
	Value string
}

// Info is what is known about one object without reading it: HeadObject's
// answer, and one row of a listing.
type Info struct {
	Key string

	// ETag is the entity tag. This cloud's IS the object's content id, so it
	// changes exactly when the content does — which is what an etag is for.
	ETag string

	Size int64

	// Modified is unix SECONDS. The vocabulary has no field finer and neither
	// does any consumer; nanoseconds would be a precision nothing sets.
	Modified int64

	// ContentType is "" when the writer named none. Applying S3's
	// application/octet-stream default is the caller's business.
	ContentType string

	// Metadata is sorted by name, so two reads of one object answer the same.
	Metadata []Metadata
}

// Object is an object and its bytes.
type Object struct {
	Info Info
	Body []byte
}

// ListRequest is ListObjectsV2's, as one value for the reason the WIT record
// exists: four loose parameters at a call site say nothing about which is which.
type ListRequest struct {
	Prefix string

	// Delimiter ends a "directory". "/" gives folders; "" lists flat.
	Delimiter string

	// MaxKeys of 0 asks for the backing's own ceiling, which is also the cap on
	// anything larger. Objects and common prefixes count against it together,
	// as S3 counts them.
	MaxKeys uint32

	// Token is what a previous truncated listing answered with.
	Token string
}

// Listing is one page.
type Listing struct {
	Objects []Info

	// CommonPrefixes are the prefixes the delimiter collapsed, sorted.
	CommonPrefixes []string

	Truncated bool

	// NextToken is handed back as ListRequest.Token. "" when nothing was
	// truncated.
	NextToken string
}

// Bucket is one object store.
type Bucket interface {
	Get(ctx context.Context, key string) (Object, error)
	Head(ctx context.Context, key string) (Info, error)

	// Put answers the etag it stored under, so a caller can record what it
	// wrote without a second call to find out.
	Put(ctx context.Context, key string, body []byte, contentType string, metadata []Metadata) (string, error)

	Delete(ctx context.Context, key string) error
	List(ctx context.Context, request ListRequest) (Listing, error)
}

// Buckets opens the one a grant names. The method is named for what it opens,
// for the reason KVs.OpenKV is.
type Buckets interface {
	OpenBucket(ctx context.Context, grant invoke.Grant) (Bucket, error)
}

// WithStorage is what backs `taubyte:storage/objects`.
func WithStorage(b Buckets) Option {
	return func(r *Runtime) { r.storage = b }
}

// storageKind is the vocabulary's own name for the kind.
const storageKind = "storage"

// storageGrantsOf is every storage grant, whatever its type.
//
// THE TYPE IS NOT FILTERED HERE, unlike a database's. A storage declaring
// `streaming` is a grant this interface cannot serve, and dropping it would
// tell the guest "no such storage" — which reads as a typo in a label that is
// perfectly correct. The composer refuses it by name instead, and what arrives
// here is a refused grant carrying the reason.
func storageGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != storageKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// servableStorage is the storage grants this tier would have to serve — the
// refused ones do not count.
//
// A REFUSED GRANT MUST NOT REFUSE THE WORKLOAD. The composer refuses a
// `streaming` storage by name, and a node with no bucket backing would
// otherwise decline to start a resource whose only storage was one it was never
// going to serve anyway — turning a per-target refusal back into a per-workload
// one, which is exactly what Grant.Refused exists to avoid.
func servableStorage(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for label, g := range storageGrantsOf(spec) {
		if g.Refused != "" {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[label] = g
	}
	return out
}

// storageImports registers the five functions for one instantiation.
func (r *Runtime) storageImports(spec invoke.Spec) []component.Option {
	grants := storageGrantsOf(spec)

	open := func(ctx context.Context, label, op string) (Bucket, *component.VariantValue) {
		grant, why := r.addressed(ctx, spec, grants, label, op)
		if why != nil {
			if why.missing {
				return nil, storageFailure(errNoSuchStorage, why.detail)
			}
			if why.unavailable {
				return nil, storageFailure(errStorageUnavailable, why.detail)
			}
			return nil, storageFailure(errStorageDenied, why.detail)
		}
		if r.storage == nil {
			return nil, storageFailure(errStorageUnavailable, "this node has nothing to open a bucket with")
		}

		bucket, err := r.storage.OpenBucket(ctx, grant)
		if err != nil {
			return nil, storageFailure(errStorageUnavailable, err.Error())
		}
		return bucket, nil
	}

	errorType := func(t *component.TypeTable) component.TypeRef {
		return t.Variant(
			component.VariantCaseSpec{Name: "no-such-storage", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-found"},
			component.VariantCaseSpec{Name: "full", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "bad-request", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)
	}

	metadataType := func(t *component.TypeTable) component.TypeRef {
		return t.Record("name", component.Prim("string"), "value", component.Prim("string"))
	}

	infoType := func(t *component.TypeTable) component.TypeRef {
		return t.Record(
			"key", component.Prim("string"),
			"etag", component.Prim("string"),
			"size", component.Prim("u64"),
			"modified", component.Prim("s64"),
			"content-type", component.Prim("string"),
			"metadata", t.List(metadataType(t)),
		)
	}

	getTbl := component.NewTypeTable()
	getFD := getTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		getTbl.Result(
			getTbl.Record("info", infoType(getTbl), "body", getTbl.List(component.Prim("u8"))),
			errorType(getTbl),
		),
	)
	get := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		bucket, refused := open(ctx, label, opObjectGet)
		if refused != nil {
			return errorResult(refused), nil
		}

		object, err := bucket.Get(ctx, key)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{
			Payload: []component.Value{infoValue(object.Info), byteList(object.Body)},
		}}, nil
	}

	headTbl := component.NewTypeTable()
	headFD := headTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		headTbl.Result(infoType(headTbl), errorType(headTbl)),
	)
	head := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		bucket, refused := open(ctx, label, opObjectHead)
		if refused != nil {
			return errorResult(refused), nil
		}

		info, err := bucket.Head(ctx, key)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: infoValue(info)}}, nil
	}

	putTbl := component.NewTypeTable()
	putFD := putTbl.Func(
		[]component.TypeRef{
			component.Prim("string"), component.Prim("string"),
			putTbl.List(component.Prim("u8")),
			component.Prim("string"),
			putTbl.List(metadataType(putTbl)),
		},
		putTbl.Result(component.Prim("string"), errorType(putTbl)),
	)
	put := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 5 {
			return nil, fmt.Errorf("storage.put: expected 5 arguments, got %d", len(args))
		}
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		body, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("storage.put: %w", err)
		}
		contentType, ok := args[3].(string)
		if !ok && args[3] != nil {
			return nil, fmt.Errorf("storage.put: content type is %T, want a string", args[3])
		}
		metadata, err := liftMetadata(args[4])
		if err != nil {
			return nil, fmt.Errorf("storage.put: %w", err)
		}

		bucket, refused := open(ctx, label, opObjectPut)
		if refused != nil {
			return errorResult(refused), nil
		}
		etag, err := bucket.Put(ctx, key, body, contentType, metadata)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: etag}}, nil
	}

	deleteTbl := component.NewTypeTable()
	deleteFD := deleteTbl.Func(
		[]component.TypeRef{component.Prim("string"), component.Prim("string")},
		deleteTbl.Result(component.TypeRef{}, errorType(deleteTbl)),
	)
	remove := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		label, key, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		bucket, refused := open(ctx, label, opObjectDelete)
		if refused != nil {
			return errorResult(refused), nil
		}
		if err := bucket.Delete(ctx, key); err != nil {
			return errorResult(storageArm(err)), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	listTbl := component.NewTypeTable()
	listFD := listTbl.Func(
		[]component.TypeRef{
			component.Prim("string"),
			listTbl.Record(
				"prefix", component.Prim("string"),
				"delimiter", component.Prim("string"),
				"max-keys", component.Prim("u32"),
				"continuation-token", component.Prim("string"),
			),
		},
		listTbl.Result(
			listTbl.Record(
				"objects", listTbl.List(infoType(listTbl)),
				"common-prefixes", listTbl.List(component.Prim("string")),
				"truncated", component.Prim("bool"),
				"next-continuation-token", component.Prim("string"),
			),
			errorType(listTbl),
		),
	)
	list := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("storage.list: expected 2 arguments, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("storage.list: label is %T, want a string", args[0])
		}
		request, err := liftListRequest(args[1])
		if err != nil {
			return nil, fmt.Errorf("storage.list: %w", err)
		}

		bucket, refused := open(ctx, label, opObjectList)
		if refused != nil {
			return errorResult(refused), nil
		}

		listing, err := bucket.List(ctx, request)
		if err != nil {
			return errorResult(storageArm(err)), nil
		}

		objects := make([]component.Value, 0, len(listing.Objects))
		for _, info := range listing.Objects {
			objects = append(objects, infoValue(info))
		}
		prefixes := make([]component.Value, 0, len(listing.CommonPrefixes))
		for _, prefix := range listing.CommonPrefixes {
			prefixes = append(prefixes, prefix)
		}
		return []component.Value{component.ResultValue{
			Payload: []component.Value{objects, prefixes, listing.Truncated, listing.NextToken},
		}}, nil
	}

	return []component.Option{
		component.WithImportCustom(storageInterface, opObjectGet, get, getFD, getTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectHead, head, headFD, headTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectPut, put, putFD, putTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectDelete, remove, deleteFD, deleteTbl.Resolver()),
		component.WithImportCustom(storageInterface, opObjectList, list, listFD, listTbl.Resolver()),
	}
}

// infoValue is one object's metadata as the record the interface declares. A
// record lowers as a flat list of its fields in declaration order.
func infoValue(info Info) component.Value {
	metadata := make([]component.Value, 0, len(info.Metadata))
	for _, entry := range info.Metadata {
		metadata = append(metadata, []component.Value{entry.Name, entry.Value})
	}
	return []component.Value{
		info.Key,
		info.ETag,
		uint64(info.Size),
		info.Modified,
		info.ContentType,
		metadata,
	}
}

// liftMetadata reads the `list<metadata>` a put carries.
func liftMetadata(v component.Value) ([]Metadata, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]component.Value)
	if !ok {
		return nil, fmt.Errorf("metadata is %T, want a list", v)
	}

	out := make([]Metadata, 0, len(list))
	for i, item := range list {
		fields, ok := item.([]component.Value)
		if !ok || len(fields) != 2 {
			return nil, fmt.Errorf("metadata %d is not a name and a value", i)
		}
		name, okName := fields[0].(string)
		value, okValue := fields[1].(string)
		if !okName || !okValue {
			return nil, fmt.Errorf("metadata %d is not two strings", i)
		}
		out = append(out, Metadata{Name: name, Value: value})
	}
	return out, nil
}

// liftListRequest reads the record a listing carries.
func liftListRequest(v component.Value) (ListRequest, error) {
	fields, ok := v.([]component.Value)
	if !ok || len(fields) != 4 {
		return ListRequest{}, fmt.Errorf("the request is %T, want four fields", v)
	}

	prefix, okPrefix := fields[0].(string)
	delimiter, okDelimiter := fields[1].(string)
	token, okToken := fields[3].(string)
	if !okPrefix || !okDelimiter || !okToken {
		return ListRequest{}, errors.New("the request's prefix, delimiter and token are not all strings")
	}

	maxKeys, err := liftU32(fields[2])
	if err != nil {
		return ListRequest{}, fmt.Errorf("max-keys: %w", err)
	}

	return ListRequest{Prefix: prefix, Delimiter: delimiter, MaxKeys: maxKeys, Token: token}, nil
}

// storageArm maps a Go error onto the arm the guest sees.
func storageArm(err error) *component.VariantValue {
	switch {
	case errors.Is(err, ErrNotFound):
		return storageFailure(errStorageNotFound, "")
	case errors.Is(err, ErrFull):
		return storageFailure(errStorageFull, err.Error())
	case errors.Is(err, ErrBadRequest):
		return storageFailure(errStorageBadRequest, err.Error())
	}
	return storageFailure(errStorageUnavailable, err.Error())
}

func storageFailure(disc uint32, detail string) *component.VariantValue {
	v := component.VariantValue{Disc: disc}
	if disc != errStorageNotFound {
		v.Payload = detail
	}
	return &v
}
