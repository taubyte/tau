package secrets

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/cloudflare/circl/group"
	"github.com/cloudflare/circl/secretsharing"
	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-datastore"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/helpers"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

const (
	keyPrefix      = "/keys/"
	recordPrefix   = "/records/"
	wrappingPrefix = "/wrappings/"
	sharePrefix    = "/shares/"
)

// KeyMeta is the public half of one distributed key: everything a client needs
// to encrypt, and everything a reader needs to know who to ask for partials.
//
// Every field is a function of the round, so every member serializes the same
// bytes. Nothing here may become node-specific -- a wall clock, a coordinator
// id -- or the record would be a key two nodes write differently.
type KeyMeta struct {
	KeyID  string `cbor:"key_id"`
	Epoch  uint64 `cbor:"epoch"`
	Public []byte `cbor:"public"`

	// Verification is the summed Feldman commitment for the round, one element
	// per coefficient. Public is its first element.
	//
	// It is what makes a single share checkable. The public key pins only the
	// dealers' constant terms, so a dealer replaying a contribution can keep its
	// constant term, change a higher coefficient, and hand back something that
	// passes the Feldman check and reproduces the published public key while
	// putting the recipient's share off the polynomial everyone else is on.
	Verification [][]byte `cbor:"verification"`

	Threshold uint     `cbor:"threshold"`
	Members   []string `cbor:"members"`
}

// VerificationVector deserializes the summed Feldman commitment.
func (k *KeyMeta) VerificationVector() ([]group.Element, error) {
	out := make([]group.Element, len(k.Verification))
	for i, b := range k.Verification {
		e := tdh.Group.NewElement()
		if err := e.UnmarshalBinary(b); err != nil {
			return nil, fmt.Errorf("deserializing verification coefficient %d: %w", i, err)
		}
		out[i] = e
	}

	return out, nil
}

// PublicKey deserializes the group public key.
func (k *KeyMeta) PublicKey() (group.Element, error) {
	pub := tdh.Group.NewElement()
	if err := pub.UnmarshalBinary(k.Public); err != nil {
		return nil, fmt.Errorf("deserializing group public key: %w", err)
	}

	return pub, nil
}

// Record is a secret at rest. The service never holds the plaintext: clients
// encrypt under the group public key before storing.
//
// The ciphertext is epoch-independent -- rotation rewrites wrappings, never
// this -- so a record is written once and read forever.
type Record struct {
	Project     string    `cbor:"project"`
	Environment string    `cbor:"environment"`
	Name        string    `cbor:"name"`
	Ciphertext  []byte    `cbor:"ciphertext"`
	CreatedAt   time.Time `cbor:"created_at"`
}

func (r *Record) Ref() Ref {
	return Ref{Project: r.Project, Environment: r.Environment, Name: r.Name}
}

// refPath is a validated reference as three storage path segments.
func refPath(ref Ref) string { return ref.String() }

// keyStore holds distributed key metadata in the shared database.
type keyStore struct{ db kvdb.KVDB }

func (s *keyStore) Put(ctx context.Context, key *KeyMeta) error {
	data, err := cbor.Marshal(key)
	if err != nil {
		return fmt.Errorf("marshaling key metadata: %w", err)
	}

	return s.db.Put(ctx, keyPrefix+key.KeyID, data)
}

func (s *keyStore) Get(ctx context.Context, keyID string) (*KeyMeta, error) {
	data, err := s.db.Get(ctx, keyPrefix+keyID)
	if err != nil {
		return nil, fmt.Errorf("getting key %s: %w", keyID, err)
	}

	var key KeyMeta
	if err := cbor.Unmarshal(data, &key); err != nil {
		return nil, fmt.Errorf("unmarshaling key %s: %w", keyID, err)
	}

	return &key, nil
}

func (s *keyStore) List(ctx context.Context) ([]*KeyMeta, error) {
	paths, err := s.db.List(ctx, keyPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing keys: %w", err)
	}

	keys := make([]*KeyMeta, 0, len(paths))
	for _, p := range paths {
		key, err := s.Get(ctx, path.Base(p))
		if err != nil {
			continue // a key mid-write is not an error for the caller
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// Newest lists every published key, newest epoch first. Rosters that differ
// during discovery can put two keys in one epoch, so the tie-break is a
// deterministic function of stored state -- widest membership, then id.
func (s *keyStore) Newest(ctx context.Context) ([]*KeyMeta, error) {
	keys, err := s.List(ctx)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(keys, func(a, b *KeyMeta) int {
		return cmp.Or(
			cmp.Compare(b.Epoch, a.Epoch),
			cmp.Compare(len(b.Members), len(a.Members)),
			cmp.Compare(a.KeyID, b.KeyID),
		)
	})

	return keys, nil
}

// Epoch is the highest epoch any key has reached, activated or not. Rotation
// counts up from here, so two nodes deciding to rotate at the same moment
// derive the same next epoch and land in the same round.
func (s *keyStore) Epoch(ctx context.Context) (uint64, error) {
	keys, err := s.List(ctx)
	if err != nil {
		return 0, err
	}

	var highest uint64
	for _, key := range keys {
		highest = max(highest, key.Epoch)
	}

	return highest, nil
}

// recordStore holds encrypted secrets in the shared database.
type recordStore struct{ db kvdb.KVDB }

func (s *recordStore) Put(ctx context.Context, record *Record) error {
	ref := record.Ref()
	if err := ref.Validate(); err != nil {
		return err
	}

	data, err := cbor.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling record: %w", err)
	}

	return s.db.Put(ctx, recordPrefix+refPath(ref), data)
}

func (s *recordStore) Get(ctx context.Context, ref Ref) (*Record, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}

	data, err := s.db.Get(ctx, recordPrefix+refPath(ref))
	if err != nil {
		return nil, fmt.Errorf("getting secret %s: %w", ref, err)
	}

	var record Record
	if err := cbor.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("unmarshaling secret %s: %w", ref, err)
	}

	return &record, nil
}

func (s *recordStore) Delete(ctx context.Context, ref Ref) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	return s.db.Delete(ctx, recordPrefix+refPath(ref))
}

// List returns the secrets of one project environment.
func (s *recordStore) List(ctx context.Context, project, environment string) ([]Ref, error) {
	if err := validScope(project, environment); err != nil {
		return nil, err
	}
	return s.list(ctx, recordPrefix+project+"/"+environment+"/")
}

// All returns every stored secret, which is what reconciliation walks.
func (s *recordStore) All(ctx context.Context) ([]Ref, error) {
	return s.list(ctx, recordPrefix)
}

func (s *recordStore) list(ctx context.Context, prefix string) ([]Ref, error) {
	paths, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}

	// Every part is validated on write, so none spans a separator and the
	// last three segments are exactly the reference.
	refs := make([]Ref, 0, len(paths))
	for _, p := range paths {
		environment := path.Dir(p)
		refs = append(refs, Ref{
			Project:     path.Base(path.Dir(environment)),
			Environment: path.Base(environment),
			Name:        path.Base(p),
		})
	}

	return refs, nil
}

// wrappingStore holds each secret's data key, sealed to one group key.
//
// Keyed by **key id, not epoch**. Two keys can share an epoch -- rosters that
// differ during discovery, or a partition where both sides count up from the
// same last-common epoch -- so an epoch does not identify the key a wrapping
// was made under. Addressing by epoch lets a reader pair a wrapping with the
// wrong group key: the AEAD rejects it, so nothing is decrypted wrongly, but a
// perfectly readable secret reports itself unreadable.
//
// The path also carries the writer, which keeps every key single-writer: two
// nodes re-encrypting the same record concurrently produce two valid, equivalent
// wrappings rather than one contended one. The writer is a stable identity, so
// re-running reconciliation replaces a node's own wrapping instead of appending
// another forever.
type wrappingStore struct{ db kvdb.KVDB }

func wrappingPath(ref Ref, keyID, writer string) string {
	return wrappingPrefix + refPath(ref) + "/" + keyID + "/" + writer
}

func (s *wrappingStore) Put(ctx context.Context, ref Ref, keyID, writer string, w *tdh.Wrapping) error {
	if err := ref.Validate(); err != nil {
		return err
	}

	data, err := cbor.Marshal(w)
	if err != nil {
		return fmt.Errorf("marshaling wrapping: %w", err)
	}

	return s.db.Put(ctx, wrappingPath(ref, keyID, writer), data)
}

// Get returns one wrapping of a secret under a key, and the writer it came from.
//
// Wrappings under one key unlock the same data key but are NOT interchangeable
// mid-decryption: each writer sealed with its own ephemeral R, and a partial is
// computed against that R. Combining partials taken against different wrappings
// yields nothing. So the writer comes back with it, and everyone contributing to
// one decryption must be pointed at the same instance.
func (s *wrappingStore) Get(ctx context.Context, ref Ref, keyID string) (*tdh.Wrapping, string, error) {
	if err := ref.Validate(); err != nil {
		return nil, "", err
	}

	paths, err := s.db.List(ctx, wrappingPrefix+refPath(ref)+"/"+keyID+"/")
	if err != nil {
		return nil, "", fmt.Errorf("listing wrappings of %s: %w", ref, err)
	}

	// Sorted, so nodes that can see the same set land on the same instance
	// without being told.
	slices.Sort(paths)

	for _, p := range paths {
		data, err := s.db.Get(ctx, p)
		if err != nil {
			continue
		}

		var w tdh.Wrapping
		if err := cbor.Unmarshal(data, &w); err != nil {
			continue
		}

		return &w, path.Base(p), nil
	}

	return nil, "", fmt.Errorf("secret %s has no wrapping under key %s", ref, keyID)
}

// GetFrom returns the wrapping one specific writer made, so every node
// contributing a partial works against the same ephemeral R.
func (s *wrappingStore) GetFrom(ctx context.Context, ref Ref, keyID, writer string) (*tdh.Wrapping, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}

	data, err := s.db.Get(ctx, wrappingPath(ref, keyID, writer))
	if err != nil {
		return nil, fmt.Errorf("secret %s has no wrapping from %s under key %s", ref, writer, keyID)
	}

	var w tdh.Wrapping
	if err := cbor.Unmarshal(data, &w); err != nil {
		return nil, fmt.Errorf("unmarshaling wrapping: %w", err)
	}

	return &w, nil
}

// KeyIDs lists the keys a secret currently has wrappings under.
func (s *wrappingStore) KeyIDs(ctx context.Context, ref Ref) ([]string, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}

	paths, err := s.db.List(ctx, wrappingPrefix+refPath(ref)+"/")
	if err != nil {
		return nil, fmt.Errorf("listing wrappings of %s: %w", ref, err)
	}

	seen := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		seen[path.Base(path.Dir(p))] = struct{}{}
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// DeleteKey removes every wrapping of a secret under one key.
func (s *wrappingStore) DeleteKey(ctx context.Context, ref Ref, keyID string) error {
	paths, err := s.db.List(ctx, wrappingPrefix+refPath(ref)+"/"+keyID+"/")
	if err != nil {
		return fmt.Errorf("listing wrappings of %s: %w", ref, err)
	}

	for _, p := range paths {
		if err := s.db.Delete(ctx, p); err != nil {
			return fmt.Errorf("deleting wrapping %s: %w", p, err)
		}
	}

	return nil
}

// DeleteAll removes every wrapping of a secret, under every key.
func (s *wrappingStore) DeleteAll(ctx context.Context, ref Ref) error {
	keyIDs, err := s.KeyIDs(ctx, ref)
	if err != nil {
		return err
	}

	for _, keyID := range keyIDs {
		if err := s.DeleteKey(ctx, ref, keyID); err != nil {
			return err
		}
	}

	return nil
}

// shareStore holds this node's key shares, on this node only. A share never
// leaves the node that derived it, so this is deliberately not the shared
// database.
type shareStore struct {
	ds     datastore.Batching
	atRest *atRest
	now    func() time.Time
}

type storedShare struct {
	ID    []byte `cbor:"id"`
	Value []byte `cbor:"value"`

	// StoredAt is this node's own clock, read only by this node. It is what
	// ages a key out for rotation; a timestamp in the shared key record would
	// be a value two nodes write differently.
	StoredAt time.Time `cbor:"stored_at"`
}

func newShareStore(root string, atRest *atRest, now func() time.Time) (*shareStore, error) {
	ds, err := helpers.NewDatastore(path.Join(root, "key"))
	if err != nil {
		return nil, fmt.Errorf("creating key share datastore: %w", err)
	}

	return &shareStore{ds: ds, atRest: atRest, now: now}, nil
}

func (s *shareStore) Put(ctx context.Context, keyID string, share secretsharing.Share) error {
	id, err := share.ID.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshaling share id: %w", err)
	}

	value, err := share.Value.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshaling share value: %w", err)
	}

	data, err := cbor.Marshal(storedShare{ID: id, Value: value, StoredAt: s.now()})
	if err != nil {
		return fmt.Errorf("marshaling share: %w", err)
	}

	// Sealed before it reaches the disk: this is the one piece of key material
	// the node cannot re-derive if somebody walks off with the directory.
	if data, err = s.atRest.seal(data); err != nil {
		return err
	}

	key := datastore.NewKey(sharePrefix + keyID)
	if err := s.ds.Put(ctx, key, data); err != nil {
		return fmt.Errorf("storing share: %w", err)
	}

	// Pebble buffers writes; a share that is not durable is a secret that
	// cannot be recovered, so pay for the sync here.
	if syncer, ok := s.ds.(interface {
		Sync(context.Context, datastore.Key) error
	}); ok {
		if err := syncer.Sync(ctx, key); err != nil {
			return fmt.Errorf("syncing share: %w", err)
		}
	}

	return nil
}

func (s *shareStore) stored(ctx context.Context, keyID string) (storedShare, error) {
	var stored storedShare

	sealed, err := s.ds.Get(ctx, datastore.NewKey(sharePrefix+keyID))
	if err != nil {
		return stored, fmt.Errorf("getting share for key %s: %w", keyID, err)
	}

	data, err := s.atRest.open(sealed)
	if err != nil {
		return stored, fmt.Errorf("share for key %s: %w", keyID, err)
	}

	if err := cbor.Unmarshal(data, &stored); err != nil {
		return stored, fmt.Errorf("unmarshaling share: %w", err)
	}

	return stored, nil
}

func (s *shareStore) Get(ctx context.Context, keyID string) (secretsharing.Share, error) {
	var share secretsharing.Share

	stored, err := s.stored(ctx, keyID)
	if err != nil {
		return share, err
	}

	share.ID = tdh.Group.NewScalar()
	if err := share.ID.UnmarshalBinary(stored.ID); err != nil {
		return share, fmt.Errorf("deserializing share id: %w", err)
	}

	share.Value = tdh.Group.NewScalar()
	if err := share.Value.UnmarshalBinary(stored.Value); err != nil {
		return share, fmt.Errorf("deserializing share value: %w", err)
	}

	return share, nil
}

// StoredAt reports when this node stored its share of a key.
func (s *shareStore) StoredAt(ctx context.Context, keyID string) (time.Time, error) {
	stored, err := s.stored(ctx, keyID)
	if err != nil {
		return time.Time{}, err
	}

	return stored.StoredAt, nil
}

func (s *shareStore) Delete(ctx context.Context, keyID string) error {
	if err := s.ds.Delete(ctx, datastore.NewKey(sharePrefix+keyID)); err != nil {
		return fmt.Errorf("deleting share of %s: %w", keyID, err)
	}

	return nil
}

func (s *shareStore) Close() error {
	if s.ds == nil {
		return nil
	}

	return s.ds.Close()
}
