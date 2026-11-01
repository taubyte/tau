package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// The browser half of messaging: a page attaches, receives what is published, and
// publishes back, with nothing deployed beside it.

// busNamed is the fqdn a bus in the fixture answers on, as the register was
// told it. The fixture holds two: `presence` with no instances block, which is
// the shape this service can serve, and `chat` with a validator, which it
// refuses.
func busNamed(t testing.TB, name, cloud string) (fqdn, id string) {
	t.Helper()

	p, err := specs.Load(compiled(t))
	require.NoError(t, err)

	// THE NAME BELONGS TO THE GATEWAY THAT ROUTES TO IT. A bus owns no host, so
	// finding one means finding the route that names it as its handler -- which
	// is the same walk the service does, from the other end.
	for _, h := range p.Hosts() {
		if h.Kind != "gateway" {
			continue
		}
		gw, _, err := p.Resource(h.Scope, h.Kind, h.Name)
		if err != nil {
			continue
		}
		handler := gw.Sub("route").Sub("handler")
		if handler.String("kind") != busKind || handler.String("name") != name {
			continue
		}
		_, resolved, err := p.Resource(h.Scope, busKind, name)
		require.NoError(t, err)

		fqdn = h.Fqdn
		if h.Generated && cloud != "" {
			fqdn += "." + cloud
		}
		return fqdn, resolved
	}
	t.Fatalf("no gateway route names a bus called %q", name)
	return "", ""
}

// listen serves this gateway on a real socket, because a websocket needs one: a
// httptest.ResponseRecorder cannot be hijacked, which is exactly the property
// crossing.Hijack exists to pass through.
func (h *harness) listen(t testing.TB) string {
	t.Helper()
	srv := httptest.NewServer(h.Gateway.Handler())
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func dial(t testing.TB, addr, host string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return websocket.DefaultDialer.Dial("ws://"+addr+"/", http.Header{"Host": []string{host}})
}

// upgradeRequest is what a browser's handshake looks like, for the refusals
// that are answered before any hijack happens.
func upgradeRequest(host string) *http.Request {
	r := httptestRequest(http.MethodGet, "/", host)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-Websocket-Version", "13")
	r.Header.Set("Sec-Websocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	return r
}

func TestABrowserGetsWhatIsPublishedToTheBus(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "presence", "")

	conn, res, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
	defer conn.Close()

	at := buses.waitForOne(t)
	at.deliver([]byte("someone came online"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, message, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, kind)
	require.Equal(t, "someone came online", string(message))
}

func TestABrowserPublishesToTheBus(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "presence", "")

	conn, _, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)
	defer conn.Close()

	at := buses.waitForOne(t)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("i am here")))

	require.Eventually(t, func() bool {
		sent := at.published()
		return len(sent) == 1 && string(sent[0]) == "i am here"
	}, 5*time.Second, 10*time.Millisecond)
}

func TestTheHandleIsTheOneAWorkloadWouldGet(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(&fakeAdmits{}))
	fqdn, id := busNamed(t, "presence", "")

	conn, _, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)
	defer conn.Close()

	buses.waitForOne(t)
	require.Equal(t, []string{specs.Handle("shop", "prod", id)}, buses.asked())
}

func TestABusIsReachedAtItsGeneratedName(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"}, WithBuses(buses), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "presence", "tau.example.io")
	require.True(t, strings.HasSuffix(fqdn, ".tau.example.io"))

	conn, res, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
	conn.Close()
}

func TestAPlainRequestToABusIsToldToUpgrade(t *testing.T) {
	h := newHarness(t, Config{}, WithBuses(&fakeBuses{}), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "presence", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, httptestRequest(http.MethodGet, "/", fqdn))
	require.Equal(t, http.StatusUpgradeRequired, w.Code)
	require.Contains(t, w.Body.String(), "websocket")
}

func TestAGatewayWithNoBusBackingRefuses(t *testing.T) {
	h := newHarness(t, Config{})
	fqdn, _ := busNamed(t, "presence", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest(fqdn))
	require.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestAnInstanceOfABusIsAttachedToByPath(t *testing.T) {
	buses := &fakeBuses{}
	admit := &fakeAdmits{}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(admit))
	fqdn, id := busNamed(t, "chat", "")

	conn, res, err := websocket.DefaultDialer.Dial(
		"ws://"+h.listen(t)+"/room-42", http.Header{"Host": []string{fqdn}})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
	defer conn.Close()

	buses.waitForOne(t)
	require.Equal(t, []string{specs.Handle("shop", "prod", id) + "/room-42"}, buses.asked(),
		"the instance is part of the handle, so a workload publishing to chat/room-42 meets this browser")

	// AND THE VALIDATOR WAS ASKED FIRST, which is this kind's only inbound
	// control: the instance name is the caller's, so the project has to have a
	// point at which it says no.
	require.Equal(t, []string{"room-42"}, admit.seen())
	require.Equal(t, "may_open", admit.settings()[0]["instances/validator/name"])
}

func TestAnInstanceTheValidatorRefusedIsNotAttached(t *testing.T) {
	buses := &fakeBuses{}
	admit := &fakeAdmits{refuse: errors.New("may_open refused it")}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(admit))
	fqdn, _ := busNamed(t, "chat", "")

	w := recorder()
	r := upgradeRequest(fqdn)
	r.URL.Path = "/room-42"
	h.Gateway.Handler().ServeHTTP(w, r)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, buses.asked(), "nothing was subscribed to")

	// AND THE REFUSAL SAYS NOTHING ABOUT THE DEPLOYMENT. Naming the function
	// would describe somebody's project to anyone who asked.
	require.NotContains(t, w.Body.String(), "may_open")
}

func TestABusIsAddressedTheWayItsBlockSaysItIs(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(&fakeAdmits{}))

	many, _ := busNamed(t, "chat", "")
	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest(many))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "holds many instances")

	one, _ := busNamed(t, "presence", "")
	w = recorder()
	r := upgradeRequest(one)
	r.URL.Path = "/room-42"
	h.Gateway.Handler().ServeHTTP(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "holds one topic")

	require.Empty(t, buses.asked())
}

func TestAPathWithTwoSegmentsIsNotAnInstance(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "chat", "")

	w := recorder()
	r := upgradeRequest(fqdn)
	r.URL.Path = "/room-42/sub"
	h.Gateway.Handler().ServeHTTP(w, r)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, buses.asked())
}

func TestABusWithInstancesNeedsAnAdmission(t *testing.T) {
	h := newHarness(t, Config{}, WithBuses(&fakeBuses{}))
	fqdn, _ := busNamed(t, "chat", "")

	w := recorder()
	r := upgradeRequest(fqdn)
	r.URL.Path = "/room-42"
	h.Gateway.Handler().ServeHTTP(w, r)

	require.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestAnUnreachableBusIsAnswered(t *testing.T) {
	buses := &fakeBuses{err: errors.New("no gossip")}
	h := newHarness(t, Config{}, WithBuses(buses), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "presence", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest(fqdn))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestAnAttachmentIsMeteredAsAnAttach(t *testing.T) {
	buses := &fakeBuses{}
	m := &meter{}
	h := newHarness(t, Config{}, WithBuses(buses), WithMeter(m), WithAdmission(&fakeAdmits{}))
	fqdn, _ := busNamed(t, "presence", "")

	conn, _, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)

	at := buses.waitForOne(t)
	at.deliver([]byte("0123456789"))

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("abc")))
	require.Eventually(t, func() bool { return len(at.published()) == 1 }, 5*time.Second, 10*time.Millisecond)

	conn.Close()

	require.Eventually(t, func() bool { return len(m.recorded()) == 1 }, 5*time.Second, 10*time.Millisecond)
	ev := m.recorded()[0]
	require.Equal(t, OpAttach, ev.Op)
	require.Equal(t, http.StatusSwitchingProtocols, ev.Status)
	require.Equal(t, "shop", ev.Project)
	require.Equal(t, "prod", ev.Environment)
	require.EqualValues(t, 3, ev.In)
	require.EqualValues(t, 10, ev.Out)
}

func TestAnUpgradeAtANameWithNoBusIsARequest(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses))

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest("ask.example.com"))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code, "`ask` is POST-only, which is the route answering")
	require.Empty(t, buses.asked(), "and nothing was attached for it")
}

// fakeAdmits records what it was asked to admit.
type fakeAdmits struct {
	mu       sync.Mutex
	asked    []string
	declared []map[string]string
	refuse   error
}

// AdmitRequest grants everything: these tests are about the attachment, and a
// guard that refused would make every one of them a test of the guard.
func (f *fakeAdmits) AdmitRequest(_ context.Context, _ specs.Address, _ *invoke.Request) (*invoke.Admission, error) {
	return &invoke.Admission{Granted: []string{"subscribe", "publish"}}, nil
}

func (f *fakeAdmits) AdmitInstance(_ context.Context, _ string, settings map[string]string, instance string) error {
	f.mu.Lock()
	f.asked = append(f.asked, instance)
	f.declared = append(f.declared, settings)
	err := f.refuse
	f.mu.Unlock()
	return err
}

func (f *fakeAdmits) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.asked...)
}

func (f *fakeAdmits) settings() []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]string(nil), f.declared...)
}

// fakeBuses records what was attached to and hands back attachments a test
// drives.
type fakeBuses struct {
	mu      sync.Mutex
	handles []string
	opened  []*fakeAttachment
	err     error
}

func (b *fakeBuses) Attach(_ context.Context, handle string) (Attachment, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	b.handles = append(b.handles, handle)
	at := &fakeAttachment{in: make(chan []byte, 8), done: make(chan struct{})}
	b.opened = append(b.opened, at)
	return at, nil
}

func (b *fakeBuses) asked() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.handles...)
}

func (b *fakeBuses) waitForOne(t testing.TB) *fakeAttachment {
	t.Helper()
	var at *fakeAttachment
	require.Eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		if len(b.opened) == 0 {
			return false
		}
		at = b.opened[0]
		return true
	}, 5*time.Second, 10*time.Millisecond)
	return at
}

type fakeAttachment struct {
	in   chan []byte
	done chan struct{}

	mu   sync.Mutex
	sent [][]byte
}

func (a *fakeAttachment) Next(ctx context.Context) ([]byte, error) {
	select {
	case m := <-a.in:
		return m, nil
	case <-a.done:
		return nil, errors.New("closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *fakeAttachment) Publish(_ context.Context, message []byte) error {
	a.mu.Lock()
	a.sent = append(a.sent, append([]byte(nil), message...))
	a.mu.Unlock()
	return nil
}

func (a *fakeAttachment) Close() error {
	select {
	case <-a.done:
	default:
		close(a.done)
	}
	return nil
}

func (a *fakeAttachment) deliver(message []byte) { a.in <- message }

func (a *fakeAttachment) published() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]byte(nil), a.sent...)
}
