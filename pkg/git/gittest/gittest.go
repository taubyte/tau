// Package gittest is an in-process git host over ssh, for tests that clone.
//
// It serves real repositories with go-git's own upload-pack server, so a clone
// against it is a real clone. It pins nothing on its own side: the host key is
// generated per server and handed to the caller, which is what a clone pins,
// and the client keys it accepts are the caller's to say — none named is any
// key, which is enough to reach a host-key check and nothing more.
package gittest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"
	gossh "golang.org/x/crypto/ssh"
)

var errRefused = errors.New("gittest: this key may not read here")

// Server is one ssh git host on loopback.
type Server struct {
	// Addr is host:port, which is also the repokey host of what it serves.
	Addr string
	// HostKey is the key it answers with; a clone pins it.
	HostKey gossh.PublicKey

	mu     sync.Mutex
	repos  map[string]storer.Storer
	allow  func(gossh.PublicKey) bool
	served int
}

// Served is how many fetches this server answered with a pack: what proves a
// refused clone read nothing, whatever the client did after.
func (s *Server) Served() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.served
}

// NewServer starts a server that accepts a client key allow says yes to. A nil
// allow accepts any key.
func NewServer(t testing.TB, allow func(gossh.PublicKey) bool) *Server {
	t.Helper()
	signer := must(gossh.NewSignerFromKey(ed25519Key()))
	s := &Server{HostKey: signer.PublicKey(), repos: map[string]storer.Storer{}, allow: allow}

	cfg := &gossh.ServerConfig{
		PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			if s.allow != nil && !s.allow(key) {
				return nil, errRefused
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	ln := must(net.Listen("tcp", "127.0.0.1:0"))
	t.Cleanup(func() { ln.Close() })
	s.Addr = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, cfg)
		}
	}()
	return s
}

// Serve publishes st at path, "/owner/name.git".
func (s *Server) Serve(path string, st storer.Storer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[path] = st
}

// URL is the ssh clone address of path.
func (s *Server) URL(path string) string { return "ssh://git@" + s.Addr + path }

// Load is server.Loader over what Serve published.
func (s *Server) Load(ep *transport.Endpoint) (storer.Storer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.repos[ep.Path]
	if !ok {
		return nil, transport.ErrRepositoryNotFound
	}
	return st, nil
}

func (s *Server) serve(conn net.Conn, cfg *gossh.ServerConfig) {
	defer conn.Close()
	sc, chans, reqs, err := gossh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go gossh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(gossh.UnknownChannelType, "session only")
			continue
		}
		ch, requests, err := nc.Accept()
		if err != nil {
			return
		}
		go s.session(ch, requests)
	}
}

// session answers one exec of git-upload-pack and nothing else.
func (s *Server) session(ch gossh.Channel, requests <-chan *gossh.Request) {
	defer ch.Close()
	for req := range requests {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := gossh.Unmarshal(req.Payload, &payload); err != nil {
			req.Reply(false, nil)
			return
		}
		command, arg, _ := strings.Cut(payload.Command, " ")
		if command != "git-upload-pack" {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)
		status := uint32(0)
		if err := s.uploadPack(ch, strings.Trim(arg, "'")); err != nil {
			ch.Stderr().Write([]byte(err.Error() + "\n"))
			status = 1
		}
		ch.SendRequest("exit-status", false, gossh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

// uploadPack is go-git's server answering one fetch, the way its own file
// transport serves one over stdio.
func (s *Server) uploadPack(ch gossh.Channel, path string) error {
	ep, err := transport.NewEndpoint(path)
	if err != nil {
		return err
	}
	session, err := server.NewServer(s).NewUploadPackSession(ep, nil)
	if err != nil {
		return err
	}
	refs, err := session.AdvertisedReferences()
	if err != nil {
		return err
	}
	if err := refs.Encode(ch); err != nil {
		return err
	}
	req := packp.NewUploadPackRequest()
	if err := req.Decode(ch); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resp, err := session.UploadPack(ctx, req)
	if err != nil {
		return err
	}
	if err := resp.Encode(ch); err != nil {
		return err
	}
	s.mu.Lock()
	s.served++
	s.mu.Unlock()
	return nil
}

// KeyPair is an ed25519 client key: the private half in OpenSSH PEM, as a
// deploy key is stored, and the public half.
func KeyPair(t testing.TB) (private string, public gossh.PublicKey) {
	t.Helper()
	priv := ed25519Key()
	block := must(gossh.MarshalPrivateKey(priv, ""))
	signer := must(gossh.NewSignerFromKey(priv))
	return string(pem.EncodeToMemory(block)), signer.PublicKey()
}

// Repository is an in-memory repository with one commit on main holding
// files, and that commit's hash.
func Repository(t testing.TB, files map[string]string) (storer.Storer, string) {
	t.Helper()
	st := memory.NewStorage()
	main := plumbing.NewBranchReferenceName("main")
	repo := must(gogit.InitWithOptions(st, memfs.New(), gogit.InitOptions{DefaultBranch: main}))
	wt := must(repo.Worktree())
	for name, body := range files {
		f := must(wt.Filesystem.Create(name))
		must(f.Write([]byte(body)))
		f.Close()
		must(wt.Add(name))
	}
	commit := must(wt.Commit("gittest", &gogit.CommitOptions{
		Author: &object.Signature{Name: "gittest", Email: "gittest@example.invalid", When: time.Unix(1700000000, 0)},
	}))
	return st, commit.String()
}

// must is for setup that cannot fail on a working machine: key generation,
// a loopback listener, an in-memory repository. Like regexp.MustCompile it
// panics rather than threading a test through each, and a panic still fails
// the test that hit it, with the stack.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ed25519Key() ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return must(priv, err)
}
