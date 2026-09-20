// Package vector backs the `vector` data model with a replicated store.
//
// # Nearest neighbour by scanning, and that is a ceiling rather than a defect
//
// There is no index. A search reads every vector in the collection and scores
// it, so the cost is the collection's size and not the answer's. For what a
// project's own retrieval set holds — a product catalogue, a documentation
// corpus, a support history — that is fast enough and needs nothing to
// provision, which is the whole point of tau's own engine.
//
// WHAT WOULD LIFT IT IS AN APPROXIMATE INDEX (HNSW or IVF), and it is a real
// piece of work rather than a flag: an index has to be built, kept in step with
// writes, and replicated or rebuilt per node. It is named here rather than
// half-built, and the shape that would replace this is the Engine interface it
// already sits behind.
//
// # The metric is the model's, not a tuning knob
//
// The vocabulary says it: cosine for a model trained on normalized embeddings,
// dot where magnitude carries meaning, euclidean for raw distance. Getting it
// wrong does not error, it just ranks badly — which is why it is declared on
// the resource and read here rather than guessed.
//
// # Every score is "higher is closer"
//
// Cosine and dot already are. Euclidean is a DISTANCE, so what is returned is
// its negative — which is the smallest transformation that keeps one rule for
// ordering and for reading a score, and is stated because -3.2 is otherwise a
// surprising number to get back.
package vector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/database"
)

// Store is the same four-method door every other engine here keeps.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Put(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// Name is the engine name the vocabulary uses for tau's own.
const Name = "distributed"

// The metrics the vocabulary declares. Omitted means cosine, which is its own
// statement rather than a default written onto every database.
const (
	Cosine    = "cosine"
	Dot       = "dot"
	Euclidean = "euclidean"
)

// The Options keys, which are the vocabulary's own attribute names.
const (
	OptionDimensions = "dimensions"
	OptionMetric     = "metric"
)

var (
	ErrNoStore     = errors.New("vector: no store to open onto")
	ErrNoID        = errors.New("vector: no id")
	ErrNotFound    = errors.New("vector: no such vector")
	ErrWidth       = errors.New("vector: that is not this database's width")
	ErrNoMetric    = errors.New("vector: no such metric")
	ErrNoDimension = errors.New("vector: this database declares no width")
)

// Engine opens vector databases over one store.
type Engine struct{ store Store }

func New(store Store) (*Engine, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	return &Engine{store: store}, nil
}

func (e *Engine) Name() string           { return Name }
func (e *Engine) Types() []database.Type { return []database.Type{database.Vector} }

// Open answers a connection scoped to one database of one deployment. An
// unaddressable spec is a connection that fails rather than an open that does —
// the registry probes with one.
func (e *Engine) Open(_ context.Context, spec database.Spec) (database.Conn, error) {
	if spec.Type != database.Vector {
		return nil, fmt.Errorf("vector: this engine backs %s and not %s", database.Vector, spec.Type)
	}

	c := &conn{store: e.store, metric: Cosine}
	c.prefix, c.err = spec.Prefix()
	if c.err != nil {
		return c, nil
	}

	if metric := spec.Options[OptionMetric]; metric != "" {
		switch metric {
		case Cosine, Dot, Euclidean:
			c.metric = metric
		default:
			c.err = fmt.Errorf("%w: %q", ErrNoMetric, metric)
			return c, nil
		}
	}

	// THE WIDTH IS REQUIRED BY THE VOCABULARY and enforced here, because a
	// 1536-wide query against 768-wide vectors is not a bad answer, it is a
	// meaningless one.
	width := spec.Options[OptionDimensions]
	if width == "" {
		c.err = ErrNoDimension
		return c, nil
	}
	n, err := strconv.Atoi(width)
	if err != nil || n < 1 {
		c.err = fmt.Errorf("%w: %q is not a width", ErrNoDimension, width)
		return c, nil
	}
	c.width = n
	return c, nil
}

type conn struct {
	store  Store
	prefix string
	metric string
	width  int
	err    error
}

var _ database.VectorConn = (*conn)(nil)

// stored is what one row holds.
type stored struct {
	Embedding []float32      `json:"embedding"`
	Meta      map[string]any `json:"meta,omitempty"`
}

// Upsert writes one vector and whatever should come back with it.
func (c *conn) Upsert(ctx context.Context, id string, embedding []float32, meta map[string]any) error {
	if c.err != nil {
		return c.err
	}
	if id == "" {
		return ErrNoID
	}
	if len(embedding) != c.width {
		return fmt.Errorf("%w: %d against %d", ErrWidth, len(embedding), c.width)
	}

	raw, err := json.Marshal(stored{Embedding: embedding, Meta: meta})
	if err != nil {
		return fmt.Errorf("vector: %s is not encodable: %w", id, err)
	}
	return c.store.Put(ctx, c.prefix+id, raw)
}

func (c *conn) Delete(ctx context.Context, id string) error {
	if c.err != nil {
		return c.err
	}
	if id == "" {
		return ErrNoID
	}
	return c.store.Delete(ctx, c.prefix+id)
}

// Search reads the collection and answers with the k closest, best first.
//
// k <= 0 IS EVERYTHING, which is what "no limit" means and what makes a small
// collection readable without a second verb.
func (c *conn) Search(ctx context.Context, query []float32, k int) ([]database.Match, error) {
	if c.err != nil {
		return nil, c.err
	}
	if len(query) != c.width {
		return nil, fmt.Errorf("%w: %d against %d", ErrWidth, len(query), c.width)
	}

	keys, err := c.store.List(ctx, c.prefix)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)

	matches := make([]database.Match, 0, len(keys))
	for _, key := range keys {
		raw, found, err := c.store.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if !found {
			continue // taken by a concurrent delete; not in the collection any more
		}

		var row stored
		if err := json.Unmarshal(raw, &row); err != nil {
			continue // a row this engine did not write
		}
		if len(row.Embedding) != len(query) {
			// A VECTOR OF ANOTHER WIDTH IS NOT SCORED. It cannot be compared,
			// and scoring it against a truncated copy would rank a wrong
			// answer above a right one.
			continue
		}

		_, id, _ := strings.Cut(key, c.prefix)
		matches = append(matches, database.Match{
			ID:    id,
			Score: score(c.metric, query, row.Embedding),
			Meta:  row.Meta,
		})
	}

	// Best first, then by id so two searches over one collection answer in the
	// same order when scores tie.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		return matches[i].ID < matches[j].ID
	})

	if k > 0 && len(matches) > k {
		matches = matches[:k]
	}
	return matches, nil
}

func (c *conn) Close() error { return nil }

// score is how close two vectors are, higher being closer. See the package doc
// for why euclidean's is negative.
func score(metric string, a, b []float32) float32 {
	switch metric {
	case Dot:
		return dot(a, b)
	case Euclidean:
		var sum float64
		for i := range a {
			d := float64(a[i]) - float64(b[i])
			sum += d * d
		}
		return float32(-math.Sqrt(sum))
	default:
		magnitudes := norm(a) * norm(b)
		if magnitudes == 0 {
			// A ZERO VECTOR HAS NO DIRECTION, so it is no distance from
			// anything rather than infinitely far — and dividing would be NaN,
			// which sorts unpredictably.
			return 0
		}
		return dot(a, b) / magnitudes
	}
}

func dot(a, b []float32) float32 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return float32(sum)
}

func norm(v []float32) float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return float32(math.Sqrt(sum))
}
