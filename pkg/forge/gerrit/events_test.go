package gerrit_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/gerrit"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// sshd is Gerrit's sshd as stream-events sees it: one exec of the stream
// command, answered with the lines a test queues, and the session held open
// until the test closes it.
type sshd struct {
	addr    string
	hostKey gossh.PublicKey

	mu       sync.Mutex
	commands []string
	lines    chan string
	refuse   string // stderr and exit when non-empty
}

func newSSHD(t *testing.T, client gossh.PublicKey) *sshd {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	require.NoError(t, err)
	d := &sshd{hostKey: signer.PublicKey(), lines: make(chan string, 16)}
	cfg := &gossh.ServerConfig{PublicKeyCallback: func(_ gossh.ConnMetadata, k gossh.PublicKey) (*gossh.Permissions, error) {
		if string(k.Marshal()) != string(client.Marshal()) {
			return nil, errRefusedKey
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	d.addr = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(conn, cfg)
		}
	}()
	return d
}

type refusedKey struct{}

func (refusedKey) Error() string { return "key refused" }

var errRefusedKey = refusedKey{}

func (d *sshd) serve(conn net.Conn, cfg *gossh.ServerConfig) {
	defer conn.Close()
	sc, chans, reqs, err := gossh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go gossh.DiscardRequests(reqs)
	for nc := range chans {
		ch, requests, err := nc.Accept()
		if err != nil {
			return
		}
		go d.session(ch, requests)
	}
}

func (d *sshd) session(ch gossh.Channel, requests <-chan *gossh.Request) {
	defer ch.Close()
	for req := range requests {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		gossh.Unmarshal(req.Payload, &payload)
		d.mu.Lock()
		d.commands = append(d.commands, payload.Command)
		refuse := d.refuse
		d.mu.Unlock()
		req.Reply(true, nil)
		if refuse != "" {
			ch.Stderr().Write([]byte(refuse + "\n"))
			ch.SendRequest("exit-status", false, gossh.Marshal(struct{ Status uint32 }{1}))
			return
		}
		for line := range d.lines {
			if line == "" {
				return // a queued "" ends the session: the server went away
			}
			if _, err := ch.Write([]byte(line + "\n")); err != nil {
				return
			}
		}
		return
	}
}

const (
	revA = "1111111111111111111111111111111111111111"
	revB = "2222222222222222222222222222222222222222"
)

func refUpdated(project, ref, rev string) string {
	return `{"type":"ref-updated","submitter":{"name":"x"},"refUpdate":{"oldRev":"` + revA + `","newRev":"` + rev + `","refName":"` + ref + `","project":"` + project + `"},"eventCreatedOn":1}`
}

func streamDriver(t *testing.T, d *sshd, host string, priv string, opts ...gerrit.Option) *forge.Registry {
	t.Helper()
	drv, err := gerrit.New(host, append(opts, gerrit.Events(d.addr, "tau-bot", []byte(priv), d.hostKey))...)
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: host, Driver: drv})
	require.NoError(t, err)
	return reg
}

func TestTheEventStreamYieldsPushesAndSkipsTheRest(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	d := newSSHD(t, pub)
	const host = "gerrit.example"
	reg := streamDriver(t, d, host, priv)
	require.Equal(t, []string{host}, reg.EventHosts())

	d.lines <- `not json`
	d.lines <- `{"type":"patchset-created"}`
	d.lines <- refUpdated("Acme/Upper", "refs/heads/main", revB)
	d.lines <- refUpdated("acme/app", "main", revB)
	d.lines <- refUpdated("acme/app", "refs/heads/dev", strings.Repeat("0", 40))
	d.lines <- refUpdated("acme/app", "refs/changes/01/1/1", revA)
	d.lines <- ""

	ready := false
	var got []forge.Push
	err := reg.StreamPushes(t.Context(), host, func() { ready = true }, func(p forge.Push, err error) {
		require.NoError(t, err)
		got = append(got, p)
	})
	require.ErrorContains(t, err, "the event stream ended")
	require.True(t, ready)
	key := repokey.Key{Host: host, Owner: "acme", Name: "app"}
	require.Equal(t, []forge.Push{
		{Key: key, Ref: "refs/heads/main", Commit: revB},
		{Key: key, Ref: "refs/changes/01/1/1", Commit: revA},
	}, got, "a bare branch is qualified, a deletion and a non-lowercase project are skipped")
	require.Equal(t, []string{"gerrit stream-events -s ref-updated"}, d.commands)
}

func TestTheEventStreamEndsWithItsContextAndSaysWhyGerritRefused(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	d := newSSHD(t, pub)
	reg := streamDriver(t, d, "gerrit.example", priv)

	// Cancelled once the subscription is accepted, with the session idle.
	sub, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- reg.StreamPushes(sub, "gerrit.example", stop, func(forge.Push, error) {})
	}()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("the stream did not end with its context")
	}

	d.mu.Lock()
	d.refuse = "fatal: not permitted: stream events"
	d.mu.Unlock()
	err := reg.StreamPushes(t.Context(), "gerrit.example", func() {}, func(forge.Push, error) {})
	require.ErrorContains(t, err, "not permitted: stream events")
}

func TestTheEventStreamRefusesAnUnpinnedHostOrAStrangeKey(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	d := newSSHD(t, pub)
	_, other := gittest.KeyPair(t)
	drv, err := gerrit.New("gerrit.example", gerrit.Events(d.addr, "tau-bot", []byte(priv), other))
	require.NoError(t, err)
	reg, err := forge.New(forge.Policy{}, forge.Binding{Host: "gerrit.example", Driver: drv})
	require.NoError(t, err)
	err = reg.StreamPushes(t.Context(), "gerrit.example", func() { t.Fatal("ready on an unpinned host") }, func(forge.Push, error) {})
	require.ErrorContains(t, err, "not pinned")

	strange, _ := gittest.KeyPair(t)
	reg = streamDriver(t, d, "gerrit.example", strange)
	require.Error(t, reg.StreamPushes(t.Context(), "gerrit.example", func() {}, func(forge.Push, error) {}))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	closed := ln.Addr().String()
	ln.Close()
	drv, err = gerrit.New("gerrit.example", gerrit.Events(closed, "tau-bot", []byte(priv), d.hostKey))
	require.NoError(t, err)
	reg, err = forge.New(forge.Policy{}, forge.Binding{Host: "gerrit.example", Driver: drv})
	require.NoError(t, err)
	require.ErrorContains(t, reg.StreamPushes(t.Context(), "gerrit.example", func() {}, func(forge.Push, error) {}), "dialing")
}

func TestEventsRefusesAnIncompleteAccount(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	_, err := gerrit.New("gerrit.example", gerrit.Events("no-port", "u", []byte(priv), pub))
	require.ErrorContains(t, err, "host:port")
	_, err = gerrit.New("gerrit.example", gerrit.Events("h:29418", "", []byte(priv), pub))
	require.ErrorContains(t, err, "no account")
	_, err = gerrit.New("gerrit.example", gerrit.Events("h:29418", "u", []byte(priv)))
	require.ErrorContains(t, err, "pinned")
	_, err = gerrit.New("gerrit.example", gerrit.Events("h:29418", "u", []byte("not a key"), pub))
	require.ErrorContains(t, err, "private key")

	d, err := gerrit.New("gerrit.example", gerrit.Membership(memberToken), gerrit.Events("h:29418", "u", []byte(priv), pub))
	require.NoError(t, err)
	_, events := d.(forge.PushEvents)
	_, members := d.(forge.Memberships)
	require.True(t, events && members)
}

func TestHeadsAreTheBranchesWithTheDeploymentsCredential(t *testing.T) {
	priv, pub := gittest.KeyPair(t)
	var auth string
	o := serve(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.EscapedPath() != "/a/projects/acme%2Fapp/branches/" {
			http.NotFound(w, r)
			return
		}
		json200(w, `[{"ref":"HEAD","revision":"main"},{"ref":"refs/meta/config","revision":"`+revA+`"},{"ref":"refs/heads/main","revision":"`+revB+`"}]`)
	}, gerrit.Membership(memberToken), gerrit.Events("127.0.0.1:1", "u", []byte(priv), pub))

	key := o.key(t, "acme/app")
	heads, err := o.reg.Heads(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, []forge.Push{{Key: key, Ref: "refs/heads/main", Commit: revB}}, heads)
	require.True(t, strings.HasPrefix(auth, "Basic "))

	_, err = o.reg.Heads(t.Context(), o.key(t, "acme/gone"))
	var missing *forge.NotFoundError
	require.ErrorAs(t, err, &missing)
}
