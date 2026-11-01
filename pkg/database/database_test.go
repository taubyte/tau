package database

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	v2 "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

func TestTheTypesAreTheVocabularysTypes(t *testing.T) {
	// DatabaseEngines is the set the vocabulary declares, keyed by model; checking against
	// it, not a copy of this package's list, catches a new data model.
	declared := map[string]bool{}
	for model := range v2.DatabaseEngines() {
		declared[model] = true
	}
	require.NotEmpty(t, declared, "the vocabulary declares no data models at all")

	known := map[string]bool{}
	for _, ty := range Types() {
		require.True(t, declared[string(ty)],
			"this package names a %q data model and the vocabulary does not — a seam for something nobody can write", ty)
		require.Equal(t, strings.ToLower(string(ty)), string(ty),
			"a type is spelled as the vocabulary spells it")
		known[string(ty)] = true
	}

	for model := range declared {
		require.True(t, known[model],
			"the vocabulary declares a %q data model and this package has no type for it, so nothing here can open one", model)
	}
}

type conn struct{ closed bool }

func (c *conn) Close() error { c.closed = true; return nil }

// kvEngine answers the kv surface.
type kvConn struct{ conn }

func (kvConn) Get(context.Context, string) ([]byte, error)          { return nil, nil }
func (kvConn) List(context.Context, string) ([]string, error)       { return nil, nil }
func (kvConn) Watch(context.Context, string) (<-chan string, error) { return nil, nil }
func (kvConn) Put(context.Context, string, []byte) error            { return nil }
func (kvConn) Delete(context.Context, string) error                 { return nil }

type sqlConn struct{ conn }

func (sqlConn) Query(context.Context, string, ...any) (Rows, error) { return nil, nil }
func (sqlConn) Exec(context.Context, string, ...any) (Outcome, error) {
	return Outcome{}, nil
}

type docConn struct{ conn }

func (docConn) Get(context.Context, string) (map[string]any, error) { return nil, nil }
func (docConn) Query(context.Context, map[string]any) ([]map[string]any, error) {
	return nil, nil
}
func (docConn) Put(context.Context, string, map[string]any) error { return nil }
func (docConn) Delete(context.Context, string) error              { return nil }

type vecConn struct{ conn }

func (vecConn) Search(context.Context, []float32, int) ([]Match, error) { return nil, nil }
func (vecConn) Upsert(context.Context, string, []float32, map[string]any) error {
	return nil
}
func (vecConn) Delete(context.Context, string) error { return nil }

// fakeEngine hands back whatever its factory says, which is how a test can
// register an engine that lies about what it implements.
type fakeEngine struct {
	name  string
	types []Type
	open  func(Spec) (Conn, error)

	mu     sync.Mutex
	opened []Spec
}

func (e *fakeEngine) Name() string  { return e.name }
func (e *fakeEngine) Types() []Type { return e.types }

func (e *fakeEngine) Open(_ context.Context, s Spec) (Conn, error) {
	e.mu.Lock()
	e.opened = append(e.opened, s)
	e.mu.Unlock()
	return e.open(s)
}

func (e *fakeEngine) specs() []Spec {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Spec(nil), e.opened...)
}

// byType hands back the right surface for whatever type is asked for, which is
// what a real multi-type engine does.
func byType(s Spec) (Conn, error) {
	switch s.Type {
	case KV:
		return &kvConn{}, nil
	case SQL:
		return &sqlConn{}, nil
	case Document:
		return &docConn{}, nil
	case Vector:
		return &vecConn{}, nil
	}
	return nil, errors.New("unknown type")
}

func tauEngine() *fakeEngine {
	return &fakeEngine{name: Tau, types: Types(), open: byType}
}

func TestAnEngineIsProvedAtRegistrationRatherThanTrusted(t *testing.T) {
	r := NewRegistry()
	e := tauEngine()

	require.NoError(t, r.Register(t.Context(), e))
	require.Equal(t, []string{Tau}, r.EnginesFor(KV))
	require.Equal(t, []string{Tau}, r.EnginesFor(SQL))

	// It was probed once per claimed type.
	require.Len(t, e.specs(), len(Types()))
}

func TestAnEngineThatClaimsATypeItCannotServeIsRefused(t *testing.T) {
	r := NewRegistry()
	// It says it does sql, and hands back a kv connection.
	liar := &fakeEngine{
		name:  "postgres",
		types: []Type{SQL},
		open:  func(Spec) (Conn, error) { return &kvConn{}, nil },
	}

	err := r.Register(t.Context(), liar)
	require.ErrorIs(t, err, ErrWrongSurface)
	require.Contains(t, err.Error(), "postgres")
	require.Contains(t, err.Error(), "sql")
	require.Empty(t, r.EnginesFor(SQL), "and nothing was registered")
}

func TestAnEngineIsRefusedForAllTypesIfOneFails(t *testing.T) {
	r := NewRegistry()
	half := &fakeEngine{
		name:  "mongo",
		types: []Type{Document, SQL}, // it can do documents; sql is a lie
		open: func(s Spec) (Conn, error) {
			if s.Type == Document {
				return &docConn{}, nil
			}
			return &conn{}, nil // implements nothing
		},
	}

	require.Error(t, r.Register(t.Context(), half))
	require.Empty(t, r.EnginesFor(Document), "the type it COULD do is not registered either")
	require.Empty(t, r.EnginesFor(SQL))
}

func TestTheProbeConnectionIsClosed(t *testing.T) {
	var conns []*kvConn
	var mu sync.Mutex
	e := &fakeEngine{name: Tau, types: []Type{KV}, open: func(Spec) (Conn, error) {
		c := &kvConn{}
		mu.Lock()
		conns = append(conns, c)
		mu.Unlock()
		return c, nil
	}}

	require.NoError(t, NewRegistry().Register(t.Context(), e))
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, conns, 1)
	require.True(t, conns[0].closed, "the probe was closed")
}

func TestTheProbeIsNotMistakenForARealDatabase(t *testing.T) {
	e := tauEngine()
	require.NoError(t, NewRegistry().Register(t.Context(), e))
	for _, s := range e.specs() {
		require.Equal(t, "probe", s.Name)
		require.Empty(t, s.Project, "a probe belongs to no project")
		require.Empty(t, s.Environment)
	}
}

func TestAnEngineThatCannotOpenAtAllIsRefused(t *testing.T) {
	r := NewRegistry()
	e := &fakeEngine{name: "postgres", types: []Type{SQL},
		open: func(Spec) (Conn, error) { return nil, errors.New("connection refused") }}

	err := r.Register(t.Context(), e)
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection refused")
	require.Contains(t, err.Error(), "postgres")
}

func TestTwoEnginesBackOneType(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), tauEngine()))
	require.NoError(t, r.Register(t.Context(), &fakeEngine{
		name: "postgres", types: []Type{SQL},
		open: func(Spec) (Conn, error) { return &sqlConn{}, nil },
	}))

	require.Equal(t, []string{Tau, "postgres"}, r.EnginesFor(SQL))
	require.Equal(t, []string{Tau}, r.EnginesFor(KV), "postgres did not claim kv")

	c, err := r.Open(t.Context(), Spec{Type: SQL, Engine: "postgres", Name: "app"})
	require.NoError(t, err)
	require.IsType(t, &sqlConn{}, c)
	require.NoError(t, c.Close())
}

func TestOpeningSomethingUnregisteredSaysWhich(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), tauEngine()))

	_, err := r.Open(t.Context(), Spec{Type: SQL, Engine: "oracle", Name: "app"})
	require.ErrorIs(t, err, ErrNoEngine)
	require.Contains(t, err.Error(), "sql/oracle")

	_, err = r.Open(t.Context(), Spec{Type: "graph", Engine: Tau})
	require.ErrorIs(t, err, ErrUnknownType)
}

func TestAnOpenFailureReachesTheCaller(t *testing.T) {
	r := NewRegistry()
	fail := false
	e := &fakeEngine{name: Tau, types: []Type{KV}, open: func(Spec) (Conn, error) {
		if fail {
			return nil, errors.New("no route to the swarm")
		}
		return &kvConn{}, nil
	}}
	require.NoError(t, r.Register(t.Context(), e))

	fail = true
	_, err := r.Open(t.Context(), Spec{Type: KV, Engine: Tau, Name: "app"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no route to the swarm")
	require.Contains(t, err.Error(), "kv/distributed")
}

func TestTheSpecCarriesTheResolvedCredentialAndNotAReference(t *testing.T) {
	e := &fakeEngine{name: "postgres", types: []Type{SQL},
		open: func(Spec) (Conn, error) { return &sqlConn{}, nil }}
	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), e))

	_, err := r.Open(t.Context(), Spec{
		Type: SQL, Engine: "postgres", Name: "app",
		Address: "db.internal:5432", Database: "app",
		Credentials: Credentials{Username: "u", Password: "p", TLS: true},
	})
	require.NoError(t, err)

	last := e.specs()[len(e.specs())-1]
	require.Equal(t, "db.internal:5432", last.Address)
	require.Equal(t, "u", last.Credentials.Username)
	require.True(t, last.Credentials.TLS,
		"TLS has no default: connecting in the clear because a field was unset is a security decision made by omission")
}

func TestRegistrationRefusesWhatItCannotUse(t *testing.T) {
	r := NewRegistry()

	require.Error(t, r.Register(t.Context(), nil))

	err := r.Register(t.Context(), &fakeEngine{types: []Type{KV}, open: byType})
	require.ErrorIs(t, err, ErrNoName)

	err = r.Register(t.Context(), &fakeEngine{name: "x", open: byType})
	require.ErrorIs(t, err, ErrNoTypes)

	err = r.Register(t.Context(), &fakeEngine{name: "x", types: []Type{"graph"}, open: byType})
	require.ErrorIs(t, err, ErrUnknownType)
	require.Contains(t, err.Error(), "graph")
}

func TestTheSameEngineTwiceIsRefused(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), tauEngine()))

	err := r.Register(t.Context(), tauEngine())
	require.ErrorIs(t, err, ErrDuplicate)
	require.Contains(t, err.Error(), Tau)
}

func TestAnOperatorCanSeeWhatThisNodeBacks(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), tauEngine()))
	require.NoError(t, r.Register(t.Context(), &fakeEngine{
		name: "postgres", types: []Type{SQL},
		open: func(Spec) (Conn, error) { return &sqlConn{}, nil },
	}))

	s := r.String()
	require.Contains(t, s, "sql: distributed, postgres")
	require.Contains(t, s, "kv: distributed")
}

func TestAnEmptyRegistryBacksNothing(t *testing.T) {
	r := NewRegistry()
	require.Empty(t, r.EnginesFor(KV))
	require.Empty(t, r.String())

	_, err := r.Open(t.Context(), Spec{Type: KV, Engine: Tau})
	require.ErrorIs(t, err, ErrNoEngine)
}

func TestConcurrentRegistrationAndLookup(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), tauEngine()))

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for range 50 {
				r.EnginesFor(KV)
				r.Open(t.Context(), Spec{Type: KV, Engine: Tau, Name: "app"})
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, []string{Tau}, r.EnginesFor(KV))
}

func TestEveryTypeHasASurfaceAndTheyAreDistinct(t *testing.T) {
	cases := map[Type]Conn{KV: &kvConn{}, SQL: &sqlConn{}, Document: &docConn{}, Vector: &vecConn{}}

	for ty, c := range cases {
		require.True(t, implements(ty, c), "%s must satisfy its own surface", ty)
		for other := range cases {
			if other == ty {
				continue
			}
			require.False(t, implements(other, c),
				"a %s connection also satisfies %s, so the registration proof proves nothing", ty, other)
		}
	}

	require.False(t, implements("graph", &kvConn{}), "an unknown type is satisfied by nothing")
	require.False(t, implements(KV, &conn{}), "a bare Conn satisfies no type")
}

func TestAnExternalEngineCanBeRegistered(t *testing.T) {
	seen := make(chan Spec, 4)

	r := NewRegistry()
	require.NoError(t, r.Register(t.Context(), &external{
		name:  "postgres",
		types: []Type{SQL},
		seen:  seen,
	}))

	require.Equal(t, []string{"postgres"}, r.EnginesFor(SQL))

	probe := <-seen
	require.NotEmpty(t, probe.Address, "an external engine was probed with nowhere to connect")
	require.Contains(t, probe.Address, ".invalid",
		"the probe address must be unresolvable, or a registration could reach something")
}

// external refuses to open without an address, which is what every engine that
// speaks to a server does.
type external struct {
	name  string
	types []Type
	seen  chan Spec
}

func (e *external) Name() string  { return e.name }
func (e *external) Types() []Type { return e.types }

func (e *external) Open(_ context.Context, spec Spec) (Conn, error) {
	select {
	case e.seen <- spec:
	default:
	}
	if spec.Address == "" {
		return nil, errors.New("an external database needs an address")
	}
	return &externalConn{}, nil
}

type externalConn struct{}

func (c *externalConn) Close() error { return nil }

func (c *externalConn) Query(context.Context, string, ...any) (Rows, error) { return nil, nil }
func (c *externalConn) Exec(context.Context, string, ...any) (Outcome, error) {
	return Outcome{}, nil
}
