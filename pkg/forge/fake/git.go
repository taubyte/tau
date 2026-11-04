package fake

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// A fake forge can also serve on loopback, on ONE port, as a forge does on one host:
// TLS (its API in the dialect its shape speaks, and git's smart HTTP), git over ssh to
// deploy keys, and git's smart HTTP in the clear. What a connection sends first says
// which: a TLS ClientHello opens with 0x16, an ssh client with its "SSH-" banner, and
// anything else is plain HTTP. A real driver bound to the host derives its https and
// ssh addresses from it, and both land here.

type Served struct {
	*Forge
	ln    net.Listener
	srv   *http.Server
	tls   *http.Server
	ssh   *sshServer
	ca    []byte
	plain *connQueue
	safe  *connQueue
	shell *connQueue
}

// Serve starts a forge of the given shape on a loopback address. The shape's Host is
// replaced by that address.
func Serve(sh Shape) (*Served, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	sh.Host = ln.Addr().String()
	f := New(sh)
	f.c.scheme = "http"

	cert, ca, err := selfSigned()
	if err != nil {
		ln.Close()
		return nil, err
	}
	s := &Served{Forge: f, ln: ln, ca: ca,
		plain: newConnQueue(ln.Addr()), safe: newConnQueue(ln.Addr()), shell: newConnQueue(ln.Addr())}
	if s.ssh, err = serveSSH(f.c, s.shell); err != nil {
		ln.Close()
		return nil, err
	}
	handler := f.c.handler()
	s.srv = &http.Server{Handler: handler}
	s.tls = &http.Server{Handler: handler, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}}
	go s.srv.Serve(s.plain)
	go s.tls.ServeTLS(s.safe, "", "")
	go s.sort()
	return s, nil
}

// CA is the root this forge's TLS chains to, PEM: what a cloud names as the forge's
// ca to trust it.
func (s *Served) CA() string { return string(s.ca) }

func (s *Served) Close() error {
	err := s.ln.Close()
	s.plain.Close()
	s.safe.Close()
	s.shell.Close()
	return errors.Join(err, s.srv.Close(), s.tls.Close())
}

// sort hands every connection to the server its first bytes name.
func (s *Served) sort() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.route(conn)
	}
}

func (s *Served) route(conn net.Conn) {
	r := bufio.NewReader(conn)
	first, err := r.Peek(1)
	if err != nil {
		conn.Close()
		return
	}
	peeked := &peekedConn{Conn: conn, r: r}
	if first[0] == 0x16 {
		s.safe.push(peeked)
		return
	}
	if banner, err := r.Peek(4); err == nil && string(banner) == "SSH-" {
		s.shell.push(peeked)
		return
	}
	s.plain.push(peeked)
}

// handler is everything this forge answers over HTTP: its API, when its shape speaks
// one, and git.
func (c *core) handler() http.Handler {
	mux := http.NewServeMux()
	if c.shape.Speaks == SpeaksGitHub {
		mux.Handle(githubAPI, http.StripPrefix(strings.TrimSuffix(githubAPI, "/"), c.github()))
	}
	mux.HandleFunc("/", c.serveGit)
	return mux
}

// peekedConn is a connection whose first bytes were read to sort it, and are read
// again by whoever it was sorted to.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// connQueue is a listener over the connections sorted to one server.
type connQueue struct {
	addr   net.Addr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newConnQueue(addr net.Addr) *connQueue {
	return &connQueue{addr: addr, conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (q *connQueue) push(c net.Conn) {
	select {
	case q.conns <- c:
	case <-q.closed:
		c.Close()
	}
}

func (q *connQueue) Accept() (net.Conn, error) {
	select {
	case c := <-q.conns:
		return c, nil
	case <-q.closed:
		return nil, net.ErrClosed
	}
}

func (q *connQueue) Close() error {
	q.once.Do(func() { close(q.closed) })
	return nil
}

func (q *connQueue) Addr() net.Addr { return q.addr }

// selfSigned is a certificate for loopback that is its own root.
func selfSigned() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake forge"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return cert, pemCert, nil
}

type gitStores struct {
	mu     sync.Mutex
	stores map[string]*memory.Storage
}

func (c *core) Load(ep *transport.Endpoint) (storer.Storer, error) {
	c.git.mu.Lock()
	defer c.git.mu.Unlock()
	st, ok := c.git.stores[ep.Path]
	if !ok {
		return nil, transport.ErrRepositoryNotFound
	}
	return st, nil
}

func (c *core) gitUser() string {
	if c.shape.Handles == HandlesUUID {
		return "x-token-auth"
	}
	return "x-access-token"
}

func (c *core) serveGit(w http.ResponseWriter, r *http.Request) {
	path, op, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), ".git/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	service := op
	if r.Method == http.MethodGet && op == "info/refs" {
		service = r.URL.Query().Get("service")
	} else if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if service != transport.UploadPackServiceName && service != transport.ReceivePackServiceName {
		http.Error(w, "smart http only", http.StatusForbidden)
		return
	}

	token := ""
	if user, password, ok := r.BasicAuth(); ok && user == c.gitUser() {
		token = password
	}
	key, err := repokey.Parse("https://" + c.shape.Host + "/" + path + ".git")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c.mu.Lock()
	rp, found := c.repos[key.String()]
	var grant forge.Access
	if found {
		grant = c.grant(rp, token)
	}
	c.mu.Unlock()
	need := forge.AccessRead
	if service == transport.ReceivePackServiceName {
		need = forge.AccessWrite
	}
	switch {
	case token == "":
		w.Header().Set("WWW-Authenticate", `Basic realm="fake"`)
		http.Error(w, "credentials required", http.StatusUnauthorized)
		return
	case !found || grant == forge.AccessNone:
		http.NotFound(w, r)
		return
	case grant < need:
		http.Error(w, "this credential may not push here", http.StatusForbidden)
		return
	}

	ep, err := transport.NewEndpoint("/" + key.FullName())
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c.ensureStore(ep)

	if err := c.gitExchange(r.Context(), w, r, ep, service, op == "info/refs"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (c *core) ensureStore(ep *transport.Endpoint) {
	c.git.mu.Lock()
	defer c.git.mu.Unlock()
	if c.git.stores == nil {
		c.git.stores = map[string]*memory.Storage{}
	}
	if c.git.stores[ep.Path] == nil {
		c.git.stores[ep.Path] = memory.NewStorage()
	}
}

func (c *core) gitExchange(ctx context.Context, w http.ResponseWriter, r *http.Request, ep *transport.Endpoint, service string, advertise bool) error {
	srv := server.NewServer(c)
	if service == transport.UploadPackServiceName {
		sess, err := srv.NewUploadPackSession(ep, nil)
		if err != nil {
			return err
		}
		defer sess.Close()
		if advertise {
			return advertiseRefs(ctx, w, sess, service)
		}
		req := packp.NewUploadPackRequest()
		if err := req.Decode(r.Body); err != nil {
			return err
		}
		resp, err := sess.UploadPack(ctx, req)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-result")
		return resp.Encode(w)
	}
	sess, err := srv.NewReceivePackSession(ep, nil)
	if err != nil {
		return err
	}
	defer sess.Close()
	if advertise {
		return advertiseRefs(ctx, w, sess, service)
	}
	req := packp.NewReferenceUpdateRequest()
	if err := req.Decode(r.Body); err != nil {
		return err
	}
	report, err := sess.ReceivePack(ctx, req)
	if report == nil {
		return err
	}
	// THE FIRST BRANCH PUSHED IS THE DEFAULT, as on a hosted forge: an empty
	// repository has no HEAD until something names one.
	if err == nil && len(req.Commands) > 0 {
		if serr := c.headFirstPush(ep, req.Commands[0].Name); serr != nil {
			return serr
		}
	}
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	return report.Encode(w)
}

func advertiseRefs(ctx context.Context, w http.ResponseWriter, sess transport.Session, service string) error {
	refs, err := sess.AdvertisedReferencesContext(ctx)
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		refs, err = packp.NewAdvRefs(), nil
	}
	if err != nil {
		return err
	}
	refs.Prefix = [][]byte{[]byte("# service=" + service), pktline.Flush}
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.Header().Set("Cache-Control", "no-cache")
	return refs.Encode(w)
}

func (c *core) headFirstPush(ep *transport.Endpoint, pushed plumbing.ReferenceName) error {
	c.git.mu.Lock()
	st := c.git.stores[ep.Path]
	c.git.mu.Unlock()
	head, err := st.Reference(plumbing.HEAD)
	if err == nil {
		if _, err := st.Reference(head.Target()); err == nil {
			return nil
		}
	}
	return st.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, pushed))
}
