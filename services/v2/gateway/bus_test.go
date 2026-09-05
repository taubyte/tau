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

	"github.com/taubyte/tau/pkg/project"
)

// bus_test is the browser half of this platform's messaging: a page attaches to
// a name, gets what is published, and publishes back, with nothing deployed
// beside it. That is the "noops" claim in the one place a user can see it.

// busNamed is the fqdn a bus in the fixture answers on, as the register was
// told it. The fixture holds two: `presence` with no instances block, which is
// the shape this service can serve, and `chat` with a validator, which it
// refuses.
func busNamed(t testing.TB, name, cloud string) (fqdn, id string) {
	t.Helper()

	p, err := project.Load(compiled(t))
	require.NoError(t, err)

	for _, h := range p.Hosts() {
		if h.Kind != busKind || h.Name != name {
			continue
		}
		_, resolved, err := p.Resource(h.Scope, h.Kind, h.Name)
		require.NoError(t, err)

		fqdn = h.Fqdn
		if h.Generated && cloud != "" {
			fqdn += "." + cloud
		}
		return fqdn, resolved
	}
	t.Fatalf("the fixture has no bus called %q", name)
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

// A BROWSER ATTACHES AND GETS WHAT IS SENT. The whole path: a Host header, the
// register, the compiled project, the bus's own handle, an upgrade, and a
// message that was published by something else entirely.
func TestABrowserGetsWhatIsPublishedToTheBus(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses))
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

// AND IT PUBLISHES BACK, because a bus is n:n and that is what attaching means.
// A read-only socket would be a different kind.
func TestABrowserPublishesToTheBus(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses))
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

// THE HANDLE IS THE ONE A WORKLOAD'S GRANT CARRIES. If these two ever disagree,
// a browser and a workload publishing to what the author wrote as one bus are
// on two topics and never meet — and nothing fails, which is the worst shape a
// defect can have.
func TestTheHandleIsTheOneAWorkloadWouldGet(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses))
	fqdn, id := busNamed(t, "presence", "")

	conn, _, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)
	defer conn.Close()

	buses.waitForOne(t)
	require.Equal(t, []string{project.Handle("shop", "prod", id)}, buses.asked())
}

// A GENERATED BUS NAME LOSES THE CLOUD'S SEGMENT, the same as every other
// reachable name — the compiled project cannot know the cloud it runs in.
func TestABusIsReachedAtItsGeneratedName(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{CloudFqdn: "tau.example.io"}, WithBuses(buses))
	fqdn, _ := busNamed(t, "presence", "tau.example.io")
	require.True(t, strings.HasSuffix(fqdn, ".tau.example.io"))

	conn, res, err := dial(t, h.listen(t), fqdn)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, res.StatusCode)
	conn.Close()
}

// A PLAIN REQUEST TO A BUS IS TOLD TO UPGRADE. 404 would be the answer for a
// name nothing serves, and this name resolves — saying so sends somebody
// looking for a typo they did not make.
func TestAPlainRequestToABusIsToldToUpgrade(t *testing.T) {
	h := newHarness(t, Config{}, WithBuses(&fakeBuses{}))
	fqdn, _ := busNamed(t, "presence", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, httptestRequest(http.MethodGet, "/", fqdn))
	require.Equal(t, http.StatusUpgradeRequired, w.Code)
	require.Contains(t, w.Body.String(), "websocket")
}

// A NODE WITH NO GOSSIP SAYS SO. Accepting the connection and delivering
// nothing would be a browser that looks attached and never updates.
func TestAGatewayWithNoBusBackingRefuses(t *testing.T) {
	h := newHarness(t, Config{})
	fqdn, _ := busNamed(t, "presence", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest(fqdn))
	require.Equal(t, http.StatusNotImplemented, w.Code)
}

// A BUS THAT ADMITS INSTANCES THROUGH A FUNCTION IS REFUSED, rather than
// attached to as if the block were not there. The fixture's `chat` is that
// shape: 200 instances, admitted by `may_open`, and a socket that ignored it
// would let anybody open any room.
func TestABusThatValidatesInstancesIsRefused(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses))
	fqdn, _ := busNamed(t, "chat", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest(fqdn))
	require.Equal(t, http.StatusNotImplemented, w.Code)
	require.Contains(t, w.Body.String(), "validator")
	require.Empty(t, buses.asked(), "and nothing was attached before it was refused")
}

// A BUS THAT CANNOT BE ATTACHED TO IS AN HTTP STATUS. Attaching happens BEFORE
// the upgrade for exactly this reason: afterwards the only thing that can be
// said is a close frame, which a browser reports as a network error.
func TestAnUnreachableBusIsAnswered(t *testing.T) {
	buses := &fakeBuses{err: errors.New("no gossip")}
	h := newHarness(t, Config{}, WithBuses(buses))
	fqdn, _ := busNamed(t, "presence", "")

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest(fqdn))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

// AN ATTACHMENT IS METERED AS ITSELF. No workload was invoked, so calling it a
// serve would inflate the invocation figure a caller checks their bill against;
// and the bytes both ways are counted, because this is the only place that sees
// them.
func TestAnAttachmentIsMeteredAsAnAttach(t *testing.T) {
	buses := &fakeBuses{}
	m := &meter{}
	h := newHarness(t, Config{}, WithBuses(buses), WithMeter(m))
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

// AN UPGRADE TO A NAME WITH NO BUS IS STILL A REQUEST. What tells the two
// apart is the request AND the name together, so a client sending upgrade
// headers at an HTTP route gets the route rather than a refusal about buses.
func TestAnUpgradeAtANameWithNoBusIsARequest(t *testing.T) {
	buses := &fakeBuses{}
	h := newHarness(t, Config{}, WithBuses(buses))

	w := recorder()
	h.Gateway.Handler().ServeHTTP(w, upgradeRequest("ask.example.com"))
	require.Equal(t, http.StatusMethodNotAllowed, w.Code, "`ask` is POST-only, which is the route answering")
	require.Empty(t, buses.asked(), "and nothing was attached for it")
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
