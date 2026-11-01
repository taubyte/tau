package gerrit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// Gerrit has no signed webhook, so the push feed is `gerrit stream-events` over
// its sshd, as the deployment's account, against a pinned host key: the channel
// is the authentication. The account needs the Stream Events capability and
// sees only events of projects it may read.

// streamCommand asks for ref updates and nothing else — a submitted change and
// a direct push both arrive as one.
const streamCommand = "gerrit stream-events -s ref-updated"

// maxEventLine bounds one event. A ref-updated event is a few hundred bytes; a
// line longer than this is not one, and ends the stream rather than growing a
// buffer without limit.
const maxEventLine = 1 << 20

type streamer struct {
	d        *driver
	addr     string // host:port of the sshd, 29418 by default
	user     string
	signer   gossh.Signer
	hostKeys []gossh.PublicKey
}

// eventDriver and memberEventDriver add the event face, for the reason
// memberDriver is its own type: forge computes capabilities by type assertion,
// and whether the operator configured a stream is decided once, at New.
type eventDriver struct {
	*driver
	*streamer
}

type memberEventDriver struct {
	*memberDriver
	*streamer
}

var (
	_ forge.PushEvents  = (*eventDriver)(nil)
	_ forge.PushEvents  = (*memberEventDriver)(nil)
	_ forge.Memberships = (*memberEventDriver)(nil)
)

// Events gives the driver the deployment's ssh account for the event stream:
// the sshd's address ("gerrit.example.com:29418"), the account's username, its
// PEM private key, and the host keys the sshd may answer with. A stream that
// accepted any host key could be fed pushes by anyone on the path, so at least
// one is required.
func Events(addr, user string, privateKeyPEM []byte, hostKeys ...gossh.PublicKey) Option {
	return func(d *driver) error {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("forge/gerrit: the event stream's address %q is not host:port", addr)
		}
		if user == "" {
			return errors.New("forge/gerrit: the event stream names no account")
		}
		if len(hostKeys) == 0 {
			return errors.New("forge/gerrit: the event stream needs the sshd's host key pinned")
		}
		signer, err := gossh.ParsePrivateKey(privateKeyPEM)
		if err != nil {
			return fmt.Errorf("forge/gerrit: the event stream's private key: %w", err)
		}
		d.stream = &streamer{d: d, addr: addr, user: user, signer: signer, hostKeys: hostKeys}
		return nil
	}
}

func (s *streamer) hostKey(_ string, _ net.Addr, key gossh.PublicKey) error {
	for _, k := range s.hostKeys {
		if k.Type() == key.Type() && bytes.Equal(k.Marshal(), key.Marshal()) {
			return nil
		}
	}
	return fmt.Errorf("forge/gerrit: %s answered with a host key that is not pinned", s.addr)
}

// StreamPushes opens one ssh session running stream-events and reads it until
// ctx ends or the session does.
func (s *streamer) StreamPushes(ctx context.Context, ready func(), emit func(forge.PushClaim)) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("forge/gerrit: dialing the event stream: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	cc, chans, reqs, err := gossh.NewClientConn(conn, s.addr, &gossh.ClientConfig{
		User:            s.user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(s.signer)},
		HostKeyCallback: s.hostKey,
	})
	if err != nil {
		return fmt.Errorf("forge/gerrit: opening the event stream: %w", err)
	}
	client := gossh.NewClient(cc, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("forge/gerrit: opening the event stream: %w", err)
	}
	defer session.Close()
	out, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Start(streamCommand); err != nil {
		return fmt.Errorf("forge/gerrit: starting the event stream: %w", err)
	}
	ready()

	lines := bufio.NewScanner(out)
	lines.Buffer(make([]byte, 0, 4096), maxEventLine)
	for lines.Scan() {
		if claim, ok := s.claim(lines.Bytes()); ok {
			emit(claim)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := lines.Err(); err != nil {
		return fmt.Errorf("forge/gerrit: reading the event stream: %w", err)
	}
	// The command ended on its own: refused, or the server went away. Wait makes
	// stderr whole; what Gerrit said is bounded before it reaches an error.
	session.Wait()
	return fmt.Errorf("forge/gerrit: the event stream ended: %s", bounded(strings.TrimSpace(stderr.String())))
}

type refUpdatedJSON struct {
	Type      string `json:"type"`
	RefUpdate struct {
		NewRev  string `json:"newRev"`
		RefName string `json:"refName"`
		Project string `json:"project"`
	} `json:"refUpdate"`
}

// claim reads one event line. Anything that is not a ref update on a project
// this layer has a key for is skipped: the stream is Gerrit's, and an event
// shape it adds later is not a reason to drop the connection.
func (s *streamer) claim(line []byte) (forge.PushClaim, bool) {
	var ev refUpdatedJSON
	if err := json.Unmarshal(line, &ev); err != nil || ev.Type != "ref-updated" {
		return forge.PushClaim{}, false
	}
	key, _, err := s.d.bind(nil).keyOf(ev.RefUpdate.Project, "a streamed event's project")
	if err != nil {
		return forge.PushClaim{}, false
	}
	return forge.PushClaim{Repository: key, Ref: qualify(ev.RefUpdate.RefName), Commit: ev.RefUpdate.NewRev}, true
}

// qualify turns the bare branch name older Gerrits put in refName into the
// refname it is; forge refuses an unqualified ref as ambiguous.
func qualify(ref string) string {
	if ref == "" || strings.HasPrefix(ref, "refs/") {
		return ref
	}
	return "refs/heads/" + ref
}

type branchJSON struct {
	Ref      string `json:"ref"`
	Revision string `json:"revision"`
}

// Heads lists a project's branches over REST, with the deployment's membership
// credential when there is one and anonymously otherwise.
func (s *streamer) Heads(ctx context.Context, key repokey.Key) ([]forge.PushClaim, error) {
	if err := s.d.Addressable(key); err != nil {
		return nil, err
	}
	c := s.d.bind(s.d.member)
	var got []branchJSON
	if err := c.send(ctx, routeBranches.at(key.FullName()), nil, &got); err != nil {
		return nil, c.classify(err, key)
	}
	out := make([]forge.PushClaim, 0, len(got))
	for _, b := range got {
		if !strings.HasPrefix(b.Ref, "refs/heads/") {
			continue
		}
		out = append(out, forge.PushClaim{Repository: key, Ref: b.Ref, Commit: b.Revision})
	}
	return out, nil
}
