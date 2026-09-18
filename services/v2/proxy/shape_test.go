package proxy_test

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/services/v2/proxy"
)

// shape_test carries real bytes through a real chain, because what a
// transformer does is change what arrives — a test that asserted on how many
// times it was called would pass for a proxy that threw the answers away.

// sieving is a transformer fake: one entry per function name, each with its own
// behaviour, and every call recorded.
type sieving struct {
	mu   sync.Mutex
	by   map[string]func(*invoke.Stream) (*invoke.Shaping, error)
	saw  []string
	last map[string][]byte
}

func newSieving() *sieving {
	return &sieving{by: map[string]func(*invoke.Stream) (*invoke.Shaping, error){}, last: map[string][]byte{}}
}

func (s *sieving) shape(at project.Address, st *invoke.Stream) (*invoke.Shaping, error) {
	s.mu.Lock()
	f, ok := s.by[at.Name]
	s.saw = append(s.saw, at.Name)
	s.last[at.Name] = append([]byte(nil), st.State...)
	s.mu.Unlock()

	if !ok {
		return nil, errors.New("no such transformer here")
	}
	return f(st)
}

func (s *sieving) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.saw...)
}

// upper passes the chunk through, uppercased. It is the simplest thing that
// proves what arrived is what the transformer produced.
func upper(st *invoke.Stream) (*invoke.Shaping, error) {
	return &invoke.Shaping{Pass: &invoke.Shaped{Out: bytes.ToUpper(st.Chunk)}}, nil
}

// lines holds bytes until a newline, which is what the STATE is for: a
// transformer that could not remember could not do this at all.
//
// IT SHAPES ONLY THE INBOUND HALF, which is also the assertion that `way`
// arrives: a fake that ignored it would uppercase the answer too, and the test
// would be unable to tell the two directions apart.
func lines(st *invoke.Stream) (*invoke.Shaping, error) {
	if st.Way != invoke.Inbound {
		return &invoke.Shaping{Pass: &invoke.Shaped{Out: st.Chunk}}, nil
	}

	held := append(append([]byte(nil), st.State...), st.Chunk...)

	var out []byte
	for {
		at := bytes.IndexByte(held, '\n')
		if at < 0 {
			break
		}
		out = append(out, bytes.ToUpper(held[:at+1])...)
		held = held[at+1:]
	}
	if st.Last {
		out = append(out, bytes.ToUpper(held)...)
		held = nil
	}
	return &invoke.Shaping{Pass: &invoke.Shaped{Out: out, State: held}}, nil
}

// shaping is a proxy whose bytes pass through the named transformers.
func shaping(t *testing.T, names []any, sv *sieving) (proxy.Socket, *workers, *warnings) {
	t.Helper()

	res := servable()
	res["transformers"] = names

	w := &workers{said: "pong:", shape: sv.shape}
	said := &warnings{}

	s, err := proxy.New(proxy.Config{CloudFqdn: "tau.example.io"},
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(w),
		proxy.WithWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)
	return sock, w, said
}

func entry(name string, fail string) map[string]any {
	e := map[string]any{"name": name}
	if fail != "" {
		e["fail"] = fail
	}
	return e
}

// TestBYTESAREWHATTHETRANSFORMERMADETHEM, in both directions.
func TestAChainShapesWhatCrossesInBothDirections(t *testing.T) {
	sv := newSieving()
	sv.by["shout"] = upper

	sock, _, _ := shaping(t, []any{entry("shout", "")}, sv)

	require.Equal(t, "PONG:PING", says(t, sock, "ping"),
		"the request went up on the way in, and the answer on the way out")

	require.Contains(t, sv.calls(), "shout")
}

// TestACHAINISAPIPELINE: what the second sees is what the first produced.
func TestASecondTransformerSeesWhatTheFirstProduced(t *testing.T) {
	sv := newSieving()
	sv.by["shout"] = upper
	sv.by["wrap"] = func(st *invoke.Stream) (*invoke.Shaping, error) {
		if len(st.Chunk) == 0 {
			return &invoke.Shaping{Pass: &invoke.Shaped{}}, nil
		}
		return &invoke.Shaping{Pass: &invoke.Shaped{Out: append([]byte("["), append(st.Chunk, ']')...)}}, nil
	}

	sock, _, _ := shaping(t, []any{entry("shout", ""), entry("wrap", "")}, sv)

	// in:  ping -> PING -> [PING];  out: pong:[PING] -> PONG:[PING] -> [PONG:[PING]]
	require.Equal(t, "[PONG:[PING]]", says(t, sock, "ping"))
}

// TestTHESTATEISCARRIEDBYTHEHOST, which is what lets a transformer buffer at
// all — and what keeps it off any one node.
func TestATransformerBuffersAcrossChunks(t *testing.T) {
	sv := newSieving()
	sv.by["lines"] = lines

	sock, _, _ := shaping(t, []any{entry("lines", "")}, sv)

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
	require.NoError(t, err)
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))

	// Three writes, one line. Nothing may reach the workload until the newline
	// does, and what reaches it is the whole line.
	for _, part := range []string{"sel", "ect", " 1\n"} {
		_, err = c.Write([]byte(part))
		require.NoError(t, err)
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, c.(*net.TCPConn).CloseWrite())

	got, _ := io.ReadAll(c)
	require.Equal(t, "pong:SELECT 1\n", string(got))
}

// TestTHELASTCHUNKFLUSHES. Whatever is held has no newline coming, so it goes
// out rather than being swallowed.
func TestTheEndOfAStreamFlushesWhatIsHeld(t *testing.T) {
	sv := newSieving()
	sv.by["lines"] = lines

	sock, _, _ := shaping(t, []any{entry("lines", "")}, sv)
	require.Equal(t, "pong:QUIT", says(t, sock, "quit"), "no newline ever arrived and the bytes still did")
}

// TestACLOSEISADECISION, not a failure, and it ends the conversation whatever
// `fail` says.
func TestATransformerThatClosesEndsTheConnection(t *testing.T) {
	sv := newSieving()
	sv.by["guard"] = func(*invoke.Stream) (*invoke.Shaping, error) {
		return &invoke.Shaping{Refused: "that is not allowed here"}, nil
	}

	sock, _, said := shaping(t, []any{entry("guard", "open")}, sv)

	require.Empty(t, says(t, sock, "ping"))
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
	require.Contains(t, said.first(), "closed the connection")
}

// TestFAILCLOSEDCUTSIT, because a filter that stopped filtering exactly when
// something was wrong is the failure the word exists to prevent.
func TestATransformerThatCannotRunAndIsFailClosedCutsTheConnection(t *testing.T) {
	sv := newSieving() // nothing registered: every call errors

	sock, w, said := shaping(t, []any{entry("audit", "closed")}, sv)

	require.Empty(t, says(t, sock, "ping"))
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)

	at, _ := w.asked()
	require.Equal(t, "db", at.Name, "the workload was dialled before the first chunk was shaped")
}

// TestFAILOPENCARRIESON, because an enrichment that took the connection down to
// add a header is the failure in the other direction.
func TestATransformerThatCannotRunAndIsFailOpenIsSkipped(t *testing.T) {
	sv := newSieving() // nothing registered: every call errors

	sock, _, _ := shaping(t, []any{entry("enrich", "open")}, sv)
	require.Equal(t, "pong:ping", says(t, sock, "ping"), "untransformed, which is what open means")
}

// TestFAILCLOSEDISTHEDEFAULT, which is what the vocabulary says and the safer
// of the two to guess wrong.
func TestAnEntryWithNoFailWordIsClosed(t *testing.T) {
	sv := newSieving()

	sock, _, said := shaping(t, []any{entry("audit", "")}, sv)
	require.Empty(t, says(t, sock, "ping"))
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

// TestEVERYLINKISASKED, even with nothing: one holding bytes may have a reason
// to release them, and a chain that skipped it would let "did anything arrive"
// decide whether the rest runs.
func TestEveryTransformerIsCalledEvenWithAnEmptyChunk(t *testing.T) {
	sv := newSieving()
	sv.by["hold"] = func(st *invoke.Stream) (*invoke.Shaping, error) {
		// Passes nothing until the stream ends, then everything.
		held := append(append([]byte(nil), st.State...), st.Chunk...)
		if !st.Last {
			return &invoke.Shaping{Pass: &invoke.Shaped{State: held}}, nil
		}
		return &invoke.Shaping{Pass: &invoke.Shaped{Out: held}}, nil
	}
	sv.by["after"] = upper

	sock, _, _ := shaping(t, []any{entry("hold", ""), entry("after", "")}, sv)

	require.Equal(t, "PONG:PING", says(t, sock, "ping"))
	require.Contains(t, sv.calls(), "after", "the second link ran on the empty chunks too")
}

// TestANENTRYWITHNONAME is skipped rather than resolved as the empty name,
// which would search every kind for nothing.
func TestAnEntryWithNoNameIsNotACallAtAll(t *testing.T) {
	sv := newSieving()

	sock, _, _ := shaping(t, []any{map[string]any{"fail": "closed"}}, sv)
	require.Equal(t, "pong:ping", says(t, sock, "ping"))
	require.Empty(t, sv.calls())
}
