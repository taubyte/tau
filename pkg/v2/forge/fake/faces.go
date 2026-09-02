package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/v2/forge"
)

// deployToken is the DEPLOYMENT's credential, baked into the driver and
// unreachable from any session. Membership is the one question a caller's own
// token cannot answer, and this is what makes that structural here too.
const deployToken = "fake-deployment-credential"

// bound is a core plus the credential a client acts as. Every face carries it;
// no method is declared on it, so embedding it in several faces of one client
// leaves no ambiguous selector.
type bound struct {
	c     *core
	token string
}

// ─────────────────────────── the mandatory client ───────────────────────────

type cBase struct{ bound }

func (b cBase) Host() string { return b.c.shape.Host }

func (b cBase) Whoami(context.Context) (forge.Identity, error) {
	if err := b.c.call("GET", "/api/user", b.token); err != nil {
		return forge.Identity{}, err
	}
	return forge.Identity{
		Login:   "fake-user",
		Subject: "fake-user-subject",
		Email:   "fake-user@example.invalid",
	}, nil
}

func (b cBase) Access(_ context.Context, k repokey.Key) (forge.Access, error) {
	if err := b.c.callAbout("GET", "/api/repos/"+k.FullName(), b.token, k); err != nil {
		if errors.Is(err, errUnreachable) && b.c.shape.faulty(FaultOutageIsRefusal) {
			return forge.AccessNone, nil
		}
		return forge.AccessNone, err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	r, ok := b.c.lookup(b.c.flatten(k))
	if !ok {
		if b.c.shape.faulty(FaultMissingIsNone) {
			return forge.AccessNone, nil
		}
		return forge.AccessNone, &forge.NotFoundError{Host: b.c.shape.Host, Key: k}
	}
	return b.c.grant(r, b.token), nil
}

func (b cBase) Repository(_ context.Context, k repokey.Key) (forge.Repository, error) {
	if err := b.c.callAbout("GET", "/api/repos/"+k.FullName(), b.token, k); err != nil {
		return forge.Repository{}, err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	r, ok := b.c.lookup(b.c.flatten(k))
	// A repository the credential holds nothing on is hidden rather than
	// refused, which is what every real forge does and what makes "no such
	// repository" and "you may not see it" one answer from outside.
	if !ok || b.c.grant(r, b.token) == forge.AccessNone {
		return forge.Repository{}, &forge.NotFoundError{Host: b.c.shape.Host, Key: k}
	}
	return b.c.repository(r, k), nil
}

// ─────────────────────────── the optional client faces ───────────────────────────

type fList struct{ bound }

func (b fList) ListRepositories(_ context.Context, yield func(forge.Repository) bool) error {
	if err := b.c.call("GET", "/api/user/repos", b.token); err != nil {
		return err
	}
	b.c.mu.Lock()
	repos := make([]forge.Repository, 0, len(b.c.repos))
	for _, k := range []repokey.Key{b.c.keys.Writable, b.c.keys.ReadOnly, b.c.keys.Admin, b.c.keys.Deep} {
		r, ok := b.c.lookup(k)
		if !ok || b.c.grant(r, b.token) == forge.AccessNone {
			continue
		}
		if b.c.addressable(k) != nil {
			continue
		}
		repos = append(repos, b.c.repository(r, k))
	}
	b.c.mu.Unlock()
	for _, r := range repos {
		if !yield(r) {
			return nil
		}
	}
	return nil
}

type fCreate struct{ bound }

func (b fCreate) Create(_ context.Context, k repokey.Key, opts forge.CreateOptions) (forge.Repository, error) {
	if err := b.c.callAbout("POST", "/api/repos/"+k.FullName(), b.token, k); err != nil {
		return forge.Repository{}, err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	if _, taken := b.c.lookup(k); taken {
		return forge.Repository{}, fmt.Errorf("fake: %q already exists", k)
	}
	if k.Namespace() != Namespace {
		return forge.Repository{}, fmt.Errorf("fake: this credential cannot create in namespace %q", k.Namespace())
	}
	r := &repo{key: k, private: opts.Private, defaultBranch: "main", grant: forge.AccessAdmin}
	b.c.repos[k.String()] = r
	return b.c.repository(r, k), nil
}

type fDelete struct{ bound }

func (b fDelete) Delete(_ context.Context, k repokey.Key) error {
	if err := b.c.callAbout("DELETE", "/api/repos/"+k.FullName(), b.token, k); err != nil {
		return err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	r, ok := b.c.lookup(k)
	if !ok {
		return &forge.NotFoundError{Host: b.c.shape.Host, Key: k}
	}
	if !b.c.grant(r, b.token).CanAdmin() {
		return fmt.Errorf("fake: deleting %q needs admin", k)
	}
	// r is what the lookup found, which is not k when a hop was followed. A
	// repository is deleted where the request went.
	delete(b.c.repos, r.key.String())
	return nil
}

type fKeys struct{ bound }

func (b fKeys) InstallDeployKey(_ context.Context, k repokey.Key, title, publicKey string) (forge.Handle, error) {
	if err := b.c.callAbout("POST", "/api/repos/"+k.FullName()+"/keys", b.token, k); err != nil {
		return "", err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	r, ok := b.c.lookup(k)
	if !ok {
		return "", &forge.NotFoundError{Host: b.c.shape.Host, Key: k}
	}
	if !b.c.grant(r, b.token).CanAdmin() {
		return "", fmt.Errorf("fake: installing a deploy key on %q needs admin", k)
	}
	// The interface has no parameter for this, so the only way a key is
	// writable is a driver reaching past the interface — which is the fault.
	readOnly := !b.c.shape.faulty(FaultWritableDeployKey)
	h := b.c.nextHandle("key")
	b.c.deploy[h] = &keyRec{key: r.key, title: title, pub: publicKey, readOnly: readOnly}
	b.c.lastRO = readOnly
	return h, nil
}

func (b fKeys) RemoveDeployKey(_ context.Context, k repokey.Key, h forge.Handle) error {
	if err := b.c.callAbout("DELETE", "/api/repos/"+k.FullName()+"/keys/"+string(h), b.token, k); err != nil {
		return err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	if _, ok := b.c.deploy[h]; !ok {
		return fmt.Errorf("fake: no deploy key %q on %q", h, k)
	}
	if b.c.shape.faulty(FaultForgetsRemoval) {
		return nil
	}
	delete(b.c.deploy, h)
	return nil
}

type fHooks struct{ bound }

func (b fHooks) RegisterPushHook(_ context.Context, k repokey.Key, callback *url.URL, wire string) (forge.Handle, error) {
	if err := b.c.callAbout("POST", "/api/repos/"+k.FullName()+"/hooks", b.token, k); err != nil {
		return "", err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	r, ok := b.c.lookup(k)
	if !ok {
		return "", &forge.NotFoundError{Host: b.c.shape.Host, Key: k}
	}
	if !b.c.grant(r, b.token).CanAdmin() {
		return "", fmt.Errorf("fake: registering a hook on %q needs admin", k)
	}
	stored := wire
	if b.c.shape.faulty(FaultManglesWire) {
		stored = strings.ToUpper(wire)
	}
	h := b.c.nextHandle("hook")
	b.c.hooks[h] = &hookRec{key: r.key, callback: callback.String(), wire: stored}
	b.c.lastWire = stored
	return h, nil
}

func (b fHooks) RemovePushHook(_ context.Context, k repokey.Key, h forge.Handle) error {
	if err := b.c.callAbout("DELETE", "/api/repos/"+k.FullName()+"/hooks/"+string(h), b.token, k); err != nil {
		return err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	if _, ok := b.c.hooks[h]; !ok {
		return fmt.Errorf("fake: no hook %q on %q", h, k)
	}
	if b.c.shape.faulty(FaultForgetsRemoval) {
		return nil
	}
	delete(b.c.hooks, h)
	return nil
}

type fAddr struct{ bound }

// CloneAddress talks to nothing: it is a fact about the host, which is why it
// keeps working while the forge is unreachable.
func (b fAddr) CloneAddress(k repokey.Key, over forge.Protocol) (forge.Address, error) {
	if over == forge.SSH {
		return forge.Address{URL: b.c.servedCloneURL(k, forge.SSH, FaultCloneAddressNamesAnother), SSHAccount: "git"}, nil
	}
	user := "x-access-token"
	if b.c.shape.Handles == HandlesUUID {
		user = "x-token-auth" // Bitbucket accepts no other basic-auth username
	}
	return forge.Address{URL: b.c.servedCloneURL(k, forge.HTTPS, FaultCloneAddressNamesAnother), HTTPUsername: user}, nil
}

type fProbe struct{ bound }

func (b fProbe) CanPush(_ context.Context, k repokey.Key, sshPrivateKey []byte) (bool, error) {
	if err := b.c.callAbout("GET", "/"+k.FullName()+".git/info/refs?service=git-receive-pack", "", k); err != nil {
		return false, err
	}
	b.c.mu.Lock()
	defer b.c.mu.Unlock()
	if _, ok := b.c.lookup(k); !ok {
		return false, &forge.NotFoundError{Host: b.c.shape.Host, Key: k}
	}
	return string(sshPrivateKey) == "fake-ssh-key-may-push", nil
}

// ─────────────────────────── the client variants ───────────────────────────
//
// A Go type either implements an interface or does not, so "which faces does
// this client have" is a choice of TYPE and not of field. Each variant below is
// one composition; there is no runtime switch and nothing that can be nil.

type cAll struct {
	cBase
	fList
	fCreate
	fDelete
	fKeys
	fHooks
	fAddr
	fProbe
}

type cNoList struct {
	cBase
	fCreate
	fDelete
	fKeys
	fHooks
	fAddr
	fProbe
}

type cNoCreate struct {
	cBase
	fList
	fDelete
	fKeys
	fHooks
	fAddr
	fProbe
}

type cNoDelete struct {
	cBase
	fList
	fCreate
	fKeys
	fHooks
	fAddr
	fProbe
}

type cNoKeys struct {
	cBase
	fList
	fCreate
	fDelete
	fHooks
	fAddr
	fProbe
}

type cNoHooks struct {
	cBase
	fList
	fCreate
	fDelete
	fKeys
	fAddr
	fProbe
}

type cNoAddr struct {
	cBase
	fList
	fCreate
	fDelete
	fKeys
	fHooks
	fProbe
}

type cNoProbe struct {
	cBase
	fList
	fCreate
	fDelete
	fKeys
	fHooks
	fAddr
}

// clientFaces are the optional client-side capabilities, in the bit order the
// variant table below is written against.
var clientFaces = []forge.Capability{
	forge.CapList, forge.CapCreate, forge.CapDelete,
	forge.CapDeployKeys, forge.CapPushHooks, forge.CapCloneAddr, forge.CapPushProbe,
}

func newClient(c *core, token string) (forge.Client, error) {
	b := bound{c: c, token: token}
	missing := make([]forge.Capability, 0, len(clientFaces))
	for _, f := range clientFaces {
		if c.shape.omits(f) {
			missing = append(missing, f)
		}
	}
	switch len(missing) {
	case 0:
		return cAll{cBase{b}, fList{b}, fCreate{b}, fDelete{b}, fKeys{b}, fHooks{b}, fAddr{b}, fProbe{b}}, nil
	case len(clientFaces):
		return cBase{b}, nil
	case 1:
		switch missing[0] {
		case forge.CapList:
			return cNoList{cBase{b}, fCreate{b}, fDelete{b}, fKeys{b}, fHooks{b}, fAddr{b}, fProbe{b}}, nil
		case forge.CapCreate:
			return cNoCreate{cBase{b}, fList{b}, fDelete{b}, fKeys{b}, fHooks{b}, fAddr{b}, fProbe{b}}, nil
		case forge.CapDelete:
			return cNoDelete{cBase{b}, fList{b}, fCreate{b}, fKeys{b}, fHooks{b}, fAddr{b}, fProbe{b}}, nil
		case forge.CapDeployKeys:
			return cNoKeys{cBase{b}, fList{b}, fCreate{b}, fDelete{b}, fHooks{b}, fAddr{b}, fProbe{b}}, nil
		case forge.CapPushHooks:
			return cNoHooks{cBase{b}, fList{b}, fCreate{b}, fDelete{b}, fKeys{b}, fAddr{b}, fProbe{b}}, nil
		case forge.CapCloneAddr:
			return cNoAddr{cBase{b}, fList{b}, fCreate{b}, fDelete{b}, fKeys{b}, fHooks{b}, fProbe{b}}, nil
		default:
			return cNoProbe{cBase{b}, fList{b}, fCreate{b}, fDelete{b}, fKeys{b}, fHooks{b}, fAddr{b}}, nil
		}
	}
	return nil, fmt.Errorf("fake: Omit %v is not a shape that exists; omit none, omit all, or omit exactly one client-side capability", missing)
}

// ─────────────────────────── the driver ───────────────────────────

type dBase struct{ c *core }

func (d dBase) Host() string { return d.c.shape.Host }

func (d dBase) Addressable(k repokey.Key) error { return d.c.addressable(k) }

func (d dBase) Bind(cred forge.Credential) (forge.Client, error) {
	if cred.IsZero() {
		return nil, forge.ErrNoCredential
	}
	return newClient(d.c, string(cred.Expose()))
}

type dSig struct{ c *core }

func (d dSig) Scheme() forge.Scheme { return d.c.shape.Scheme }

// Push reads a payload the core has ALREADY verified — the parameter type is
// the proof — and returns what it CLAIMS. It never decides anything.
func (d dSig) Push(body forge.VerifiedBody, header http.Header) (forge.PushClaim, error) {
	if header.Get("X-Fake-Event") != "push" {
		return forge.PushClaim{}, forge.ErrNotAPush
	}
	var p payload
	if err := json.Unmarshal(body.Bytes(), &p); err != nil {
		return forge.PushClaim{}, fmt.Errorf("fake: unreadable push payload: %w", err)
	}
	spelling := p.Repository.CloneURL
	if !d.c.shape.PayloadCarriesCloneURL {
		// Bitbucket's payload names the repository by owner/name and leaves the
		// host to the driver, which is the driver's own — never the payload's.
		spelling = "https://" + d.c.shape.Host + "/" + p.Repository.FullName
	}
	k, err := repokey.Parse(spelling)
	if err != nil {
		return forge.PushClaim{}, fmt.Errorf("fake: push payload names no repository: %w", err)
	}
	return forge.PushClaim{Repository: k, Ref: p.Ref, Commit: p.After}, nil
}

type dMem struct{ c *core }

// IsActiveMember spends the DEPLOYMENT's credential, which no session can
// reach.
func (d dMem) IsActiveMember(_ context.Context, namespace, username string) (bool, error) {
	if err := d.c.call("GET", "/api/orgs/"+namespace+"/members/"+username, deployToken); err != nil {
		return false, err
	}
	return namespace == Namespace && username == Member, nil
}

type dAll struct {
	dBase
	dSig
	dMem
}

type dSigOnly struct {
	dBase
	dSig
}

type dMemOnly struct {
	dBase
	dMem
}

type dNone struct{ dBase }

func newDriver(c *core) forge.Driver {
	sig := !c.shape.omits(forge.CapSignatures)
	mem := !c.shape.omits(forge.CapMembership)
	base := dBase{c}
	switch {
	case sig && mem:
		return dAll{base, dSig{c}, dMem{c}}
	case sig:
		return dSigOnly{base, dSig{c}}
	case mem:
		return dMemOnly{base, dMem{c}}
	default:
		return dNone{base}
	}
}

// payload is this forge's push event. It carries both spellings so one struct
// serves both shapes; which one is read is the shape's decision.
type payload struct {
	Repository struct {
		CloneURL string `json:"clone_url"`
		FullName string `json:"full_name"`
	} `json:"repository"`
	Ref   string `json:"ref"`
	After string `json:"after"`
}

// ─────────────────────────── deliveries ───────────────────────────

// Delivery builds a REAL, correctly signed push delivery for a hook this forge
// holds — real HMAC over real bytes, produced the way the forge would produce
// it, so a consumer testing verification is testing verification.
func (f *Forge) Delivery(h forge.Hook, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, h.Key, ref, commit, time.Now(), true)
}

// DeliveryFor builds one naming a DIFFERENT repository under this hook's own
// secret and scheme: a delivery that is perfectly signed and speaks for
// somebody else's repository.
func (f *Forge) DeliveryFor(h forge.Hook, other repokey.Key, ref, commit string) (forge.Delivery, error) {
	return f.deliver(h, other, ref, commit, time.Now(), true)
}

// NonPushDelivery is a correctly signed delivery that is not a push.
func (f *Forge) NonPushDelivery(h forge.Hook) (forge.Delivery, error) {
	return f.deliver(h, h.Key, "", "", time.Now(), false)
}

// DeliveryAt dates a delivery, which only a scheme that signs a timestamp
// notices — and which is what the replay window is asserted with.
func (f *Forge) DeliveryAt(h forge.Hook, ref, commit string, at time.Time) (forge.Delivery, error) {
	return f.deliver(h, h.Key, ref, commit, at, true)
}

func (f *Forge) deliver(h forge.Hook, names repokey.Key, ref, commit string, at time.Time, isPush bool) (forge.Delivery, error) {
	f.c.mu.Lock()
	rec, ok := f.c.hooks[h.Handle]
	f.c.mu.Unlock()
	if !ok {
		return forge.Delivery{}, fmt.Errorf("fake: no hook %q to deliver to", h.Handle)
	}

	var p payload
	p.Repository.CloneURL = f.c.cloneURL(names, forge.HTTPS)
	p.Repository.FullName = names.FullName()
	p.Ref = ref
	p.After = commit
	body, err := json.Marshal(p)
	if err != nil {
		return forge.Delivery{}, err
	}

	scheme := h.Scheme
	if f.c.shape.faulty(FaultSignsWithAnotherScheme) {
		scheme = otherScheme(scheme)
	}
	// The forge holds the WIRE string, not the secret. When a driver mangled
	// the wire on the way in, the forge signs with what it was actually given —
	// which is how a mangled registration shows up as a delivery nothing can
	// verify, rather than as nothing at all.
	secret := h.Secret
	if want, err := h.Scheme.Wire(h.Secret); err != nil || want != rec.wire {
		secret = forge.SecretFrom(padded(rec.wire))
	}
	hdr, err := forge.Sign(scheme, secret, body, "msg_"+string(h.Handle), at)
	if err != nil {
		return forge.Delivery{}, err
	}
	if isPush {
		hdr.Set("X-Fake-Event", "push")
	} else {
		hdr.Set("X-Fake-Event", "ping")
	}
	return forge.Delivery{Header: hdr, Body: body}, nil
}

// padded makes any string usable as a secret for every scheme, including the
// one that insists on exactly SecretSize bytes.
func padded(s string) []byte {
	b := make([]byte, forge.SecretSize)
	copy(b, s)
	return b
}

func otherScheme(s forge.Scheme) forge.Scheme {
	if s == forge.SchemeHubSHA256 {
		return forge.SchemeBitbucketSHA256
	}
	return forge.SchemeHubSHA256
}
