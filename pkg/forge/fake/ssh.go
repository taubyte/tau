package fake

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// ssh.go serves a served forge's repositories over ssh, to deploy keys only:
// a connection reads a repository when the key it signed with is one installed
// on that repository, and nothing pushes, because deploy keys here are
// read-only.

type sshServer struct {
	ln     net.Listener
	config *gossh.ServerConfig
	host   gossh.Signer
}

const sshKeyExtension = "fake-public-key"

func serveSSH(c *core, ln net.Listener) (*sshServer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	config := &gossh.ServerConfig{
		// Every key is let in; which repository it may read is decided per
		// command, against the deploy keys installed on that repository.
		PublicKeyCallback: func(_ gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
			return &gossh.Permissions{Extensions: map[string]string{sshKeyExtension: string(key.Marshal())}}, nil
		},
	}
	config.AddHostKey(signer)
	s := &sshServer{ln: ln, config: config, host: signer}
	go s.accept(c)
	return s, nil
}

// HostKey is the ssh host key this forge answers with, as an authorized_keys
// line, for pinning.
func (s *Served) HostKey() string {
	return strings.TrimSpace(string(gossh.MarshalAuthorizedKey(s.ssh.host.PublicKey())))
}

func (s *sshServer) accept(c *core) {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c, conn)
	}
}

func (s *sshServer) handle(c *core, nc net.Conn) {
	defer nc.Close()
	conn, chans, reqs, err := gossh.NewServerConn(nc, s.config)
	if err != nil {
		return
	}
	defer conn.Close()
	go gossh.DiscardRequests(reqs)
	pub := []byte(conn.Permissions.Extensions[sshKeyExtension])
	for nch := range chans {
		if nch.ChannelType() != "session" {
			nch.Reject(gossh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go c.sshSession(ch, creqs, pub)
	}
}

func (c *core) sshSession(ch gossh.Channel, reqs <-chan *gossh.Request, pub []byte) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		type execRequest struct{ Command string }
		var cmd execRequest
		if err := gossh.Unmarshal(req.Payload, &cmd); err != nil {
			req.Reply(false, nil)
			return
		}
		req.Reply(true, nil)
		status := uint32(0)
		if err := c.sshExec(ch, cmd.Command, pub); err != nil {
			fmt.Fprintln(ch.Stderr(), err)
			status = 1
		}
		type exitStatus struct{ Status uint32 }
		ch.SendRequest("exit-status", false, gossh.Marshal(exitStatus{status}))
		return
	}
}

func (c *core) sshExec(ch gossh.Channel, command string, pub []byte) error {
	service, arg, ok := strings.Cut(command, " ")
	if !ok {
		return errors.New("fake: not a git command")
	}
	if service == transport.ReceivePackServiceName {
		return errors.New("fake: a deploy key is read-only")
	}
	if service != transport.UploadPackServiceName {
		return errors.New("fake: git only")
	}
	path := strings.TrimSuffix(strings.TrimPrefix(strings.Trim(arg, "'"), "/"), ".git")
	ep, err := c.deployKeyReads(path, pub)
	if err != nil {
		return err
	}
	c.ensureStore(ep)
	return c.sshUploadPack(context.Background(), ch, ep)
}

// deployKeyReads is the repository path names, when pub is a deploy key
// installed on it.
func (c *core) deployKeyReads(path string, pub []byte) (*transport.Endpoint, error) {
	notFound := fmt.Errorf("fake: repository %q not found", path)
	k, err := repokey.Parse("https://" + c.shape.Host + "/" + path + ".git")
	if err != nil {
		return nil, notFound
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.lookup(k)
	if !ok {
		return nil, notFound
	}
	for _, rec := range c.deploy {
		if rec.key != r.key {
			continue
		}
		installed, _, _, _, err := gossh.ParseAuthorizedKey([]byte(rec.pub))
		if err == nil && bytes.Equal(installed.Marshal(), pub) {
			return transport.NewEndpoint("/" + r.key.FullName())
		}
	}
	return nil, notFound
}

func (c *core) sshUploadPack(ctx context.Context, ch io.ReadWriter, ep *transport.Endpoint) error {
	sess, err := server.NewServer(c).NewUploadPackSession(ep, nil)
	if err != nil {
		return err
	}
	defer sess.Close()
	refs, err := sess.AdvertisedReferencesContext(ctx)
	if err != nil {
		return err
	}
	if err := refs.Encode(ch); err != nil {
		return err
	}
	in := bufio.NewReader(ch)
	head, err := in.Peek(4)
	if errors.Is(err, io.EOF) || (err == nil && bytes.Equal(head, pktline.FlushPkt)) {
		return nil
	}
	if err != nil {
		return err
	}
	req := packp.NewUploadPackRequest()
	if err := req.Decode(in); err != nil {
		return err
	}
	// Decode stops at the wants. The client then writes its haves and done
	// and closes its input, and fails the fetch with EOF if this side hung up
	// first, so the answer waits for that close, as pkg/git/gittest's does.
	// The haves are dropped: the pack is still everything wanted.
	if _, err := io.Copy(io.Discard, in); err != nil {
		return err
	}
	resp, err := sess.UploadPack(ctx, req)
	if err != nil {
		return err
	}
	return resp.Encode(ch)
}
