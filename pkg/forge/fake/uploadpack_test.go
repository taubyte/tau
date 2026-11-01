package fake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/require"
)

// uploadClient is the client end of an ssh upload-pack session that sends its
// request in parts, one per read, and records the order the server reads and
// writes in.
type uploadClient struct {
	parts  []part
	events []string
	out    bytes.Buffer
	// end is what a read past the last part returns; nil is the client
	// closing its input, io.EOF.
	end error
}

type part struct {
	name string
	data []byte
}

func (c *uploadClient) Read(p []byte) (int, error) {
	if len(c.parts) == 0 {
		if c.end != nil {
			return 0, c.end
		}
		c.events = append(c.events, "read the close")
		return 0, io.EOF
	}
	n := copy(p, c.parts[0].data)
	c.parts[0].data = c.parts[0].data[n:]
	c.events = append(c.events, "read "+c.parts[0].name)
	if len(c.parts[0].data) == 0 {
		c.parts = c.parts[1:]
	}
	return n, nil
}

func (c *uploadClient) Write(p []byte) (int, error) {
	if len(c.events) == 0 || c.events[len(c.events)-1] != "write" {
		c.events = append(c.events, "write")
	}
	return c.out.Write(p)
}

func pkts(t *testing.T, write func(*pktline.Encoder) error) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, write(pktline.NewEncoder(&b)))
	return b.Bytes()
}

func commitInto(t *testing.T, st *memory.Storage) plumbing.Hash {
	t.Helper()
	repo, err := gogit.Init(st, memfs.New())
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	f, err := wt.Filesystem.Create("README")
	require.NoError(t, err)
	_, err = f.Write([]byte("hi"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	_, err = wt.Add("README")
	require.NoError(t, err)
	at := &object.Signature{Name: "t", Email: "t@example.com", When: time.Unix(1700000000, 0)}
	h, err := wt.Commit("first", &gogit.CommitOptions{Author: at, Committer: at})
	require.NoError(t, err)
	return h
}

func TestUploadPackOverSSHAnswersOnlyOnceTheRequestHasEnded(t *testing.T) {
	c := New(GitHubLike).c
	ep, err := transport.NewEndpoint("/acme/repo")
	require.NoError(t, err)
	c.ensureStore(ep)
	head := commitInto(t, c.git.stores[ep.Path])

	client := &uploadClient{parts: []part{
		{"wants", pkts(t, func(e *pktline.Encoder) error {
			if err := e.Encodef("want %s ofs-delta\n", head); err != nil {
				return err
			}
			return e.Flush()
		})},
		{"done", pkts(t, func(e *pktline.Encoder) error { return e.Encodef("done\n") })},
	}}
	require.NoError(t, c.sshUploadPack(context.Background(), client, ep))

	require.Equal(t, []string{"write", "read wants", "read done", "read the close", "write"}, client.events,
		"the refs, then the whole request and its close, and only then the pack")
	require.Contains(t, client.out.String(), "PACK")
}

func TestUploadPackOverSSHAnswersNothingToABrokenRequest(t *testing.T) {
	c := New(GitHubLike).c
	ep, err := transport.NewEndpoint("/acme/repo")
	require.NoError(t, err)
	c.ensureStore(ep)
	head := commitInto(t, c.git.stores[ep.Path])

	broken := errors.New("connection reset")
	client := &uploadClient{end: broken, parts: []part{
		{"wants", pkts(t, func(e *pktline.Encoder) error {
			if err := e.Encodef("want %s ofs-delta\n", head); err != nil {
				return err
			}
			return e.Flush()
		})},
	}}
	require.ErrorIs(t, c.sshUploadPack(context.Background(), client, ep), broken)
	require.NotContains(t, client.out.String(), "PACK")
}
