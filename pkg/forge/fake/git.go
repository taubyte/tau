package fake

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"

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

// A fake forge can also serve git's smart HTTP protocol on loopback, go-git
// behind it; a clone and a push take the grants every face answers with.

type Served struct {
	*Forge
	srv *http.Server
	ssh *sshServer
}

// Serve starts a forge of the given shape whose host is a loopback address
// serving git over plain http. The shape's Host is replaced by that address.
func Serve(sh Shape) (*Served, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	sh.Host = ln.Addr().String()
	f := New(sh)
	f.c.scheme = "http"
	ssh, err := serveSSH(f.c)
	if err != nil {
		ln.Close()
		return nil, err
	}
	s := &Served{Forge: f, srv: &http.Server{Handler: http.HandlerFunc(f.c.serveGit)}, ssh: ssh}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *Served) Close() error {
	return errors.Join(s.srv.Close(), s.ssh.ln.Close())
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
