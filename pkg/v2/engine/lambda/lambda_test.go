package lambda

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/v2/engine"
)

// guest is a minimal conforming Lambda runtime: it polls next, runs a handler,
// and posts the result back. Every test below drives the real HTTP contract
// through it rather than calling the handlers directly, because the contract IS
// the product here — a mistake in it means the image does not run.
type guest struct {
	t      *testing.T
	base   string
	handle func(ev []byte) (status int, body string, fail string)
}

func (g *guest) runOnce() {
	g.t.Helper()

	resp, err := http.Get(g.base + pathNext)
	require.NoError(g.t, err)
	defer resp.Body.Close()
	require.Equal(g.t, http.StatusOK, resp.StatusCode, "a non-200 from next is fatal to a real runtime")

	id := resp.Header.Get(headerRequestID)
	require.NotEmpty(g.t, id)
	require.NotEmpty(g.t, resp.Header.Get(headerDeadline))
	require.NotEmpty(g.t, resp.Header.Get(headerARN))

	ev, err := io.ReadAll(resp.Body)
	require.NoError(g.t, err)

	status, body, fail := g.handle(ev)
	if fail != "" {
		post(g.t, g.base+invocationPfx+id+"/error", fail)
		return
	}
	out, err := json.Marshal(lambdaResponse{StatusCode: status, Body: body})
	require.NoError(g.t, err)
	post(g.t, g.base+invocationPfx+id+"/response", string(out))
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

func newAPI(t *testing.T, opts Options) (*API, string) {
	t.Helper()
	a := New(opts)
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return a, srv.URL
}

// TestAGuestPollsAndAnswers is the whole contract in one test.
func TestAGuestPollsAndAnswers(t *testing.T) {
	a, base := newAPI(t, Options{})

	g := &guest{t: t, base: base, handle: func(ev []byte) (int, string, string) {
		var e event
		require.NoError(t, json.Unmarshal(ev, &e))
		require.Equal(t, "2.0", e.Version)
		require.Equal(t, "POST", e.RequestContext.HTTP.Method)
		require.Equal(t, "/checkout", e.RawPath)
		require.Equal(t, "qty=2", e.RawQueryString)
		require.Equal(t, "application/json", e.Headers["content-type"])
		require.Equal(t, `{"item":"hat"}`, e.Body)
		require.False(t, e.IsBase64Encoded)
		return 201, `{"ok":true}`, ""
	}}
	go g.runOnce()

	resp, err := a.Invoke(context.Background(), &engine.Request{
		Method:  "POST",
		Path:    "/checkout?qty=2",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    []byte(`{"item":"hat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, 201, resp.Status)
	require.Equal(t, `{"ok":true}`, string(resp.Body))
}

// TestNextBlocksUntilThereIsWork. Returning 204 or an empty body to say
// "nothing yet" makes a conforming runtime spin, and the AWS runtimes treat a
// non-200 from next as fatal.
func TestNextBlocksUntilThereIsWork(t *testing.T) {
	a, base := newAPI(t, Options{})

	got := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get(base + pathNext)
		if err == nil {
			got <- resp
		}
	}()

	select {
	case <-got:
		t.Fatal("next answered before there was any work")
	case <-time.After(100 * time.Millisecond):
	}

	go func() {
		a.Invoke(context.Background(), &engine.Request{Path: "/"})
	}()

	select {
	case resp := <-got:
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.NotEmpty(t, resp.Header.Get(headerRequestID))
	case <-time.After(3 * time.Second):
		t.Fatal("next never woke up when work arrived")
	}
}

// TestAGuestThatArrivesLateStillGetsTheWork — the invocation is parked, not
// dropped, when nothing is polling yet.
func TestAGuestThatArrivesLateStillGetsTheWork(t *testing.T) {
	a, base := newAPI(t, Options{})

	done := make(chan *engine.Response, 1)
	go func() {
		resp, err := a.Invoke(context.Background(), &engine.Request{Path: "/late"})
		require.NoError(t, err)
		done <- resp
	}()

	time.Sleep(50 * time.Millisecond) // the guest is slow to come up

	g := &guest{t: t, base: base, handle: func(ev []byte) (int, string, string) {
		return 200, "late but served", ""
	}}
	g.runOnce()

	select {
	case resp := <-done:
		require.Equal(t, "late but served", string(resp.Body))
	case <-time.After(3 * time.Second):
		t.Fatal("the parked invocation was never delivered")
	}
}

// TestAHandledErrorReachesTheCaller.
func TestAHandledErrorReachesTheCaller(t *testing.T) {
	a, base := newAPI(t, Options{})

	g := &guest{t: t, base: base, handle: func([]byte) (int, string, string) {
		return 0, "", `{"errorType":"TypeError","errorMessage":"x is not a function"}`
	}}
	go g.runOnce()

	_, err := a.Invoke(context.Background(), &engine.Request{Path: "/"})
	require.ErrorIs(t, err, ErrGuestFailed)
	require.Contains(t, err.Error(), "x is not a function",
		"the handler's own message is what somebody debugging needs")
}

// TestAnInitErrorFailsEveryInvocationImmediately. A guest that said it could
// not start is not going to answer, so waiting out the timeout tells nobody
// anything.
func TestAnInitErrorFailsEveryInvocationImmediately(t *testing.T) {
	a, base := newAPI(t, Options{Timeout: time.Hour})

	post(t, base+pathInitError, `{"errorMessage":"cannot find module 'handler'"}`)
	require.ErrorIs(t, a.InitError(), ErrInitFailed)

	start := time.Now()
	_, err := a.Invoke(context.Background(), &engine.Request{Path: "/"})
	require.ErrorIs(t, err, ErrInitFailed)
	require.Contains(t, err.Error(), "cannot find module")
	require.Less(t, time.Since(start), time.Second, "it did not wait out the hour")
}

// TestAnInitErrorDuringAnInvocationEndsIt.
func TestAnInitErrorDuringAnInvocationEndsIt(t *testing.T) {
	a, base := newAPI(t, Options{Timeout: time.Hour})

	done := make(chan error, 1)
	go func() {
		_, err := a.Invoke(context.Background(), &engine.Request{Path: "/"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)

	post(t, base+pathInitError, `{"errorMessage":"died on boot"}`)
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrInitFailed)
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight invocation was left hanging")
	}
}

// TestAnAnswerToAnOldInvocationIsRefused. This is the worst version of the bug
// this guards: a well-formed response that belongs to somebody else's request.
func TestAnAnswerToAnOldInvocationIsRefused(t *testing.T) {
	a, base := newAPI(t, Options{})

	// Capture a live id, then let that invocation be abandoned.
	ctx, cancel := context.WithCancel(context.Background())
	ids := make(chan string, 1)
	go func() {
		resp, err := http.Get(base + pathNext)
		require.NoError(t, err)
		defer resp.Body.Close()
		ids <- resp.Header.Get(headerRequestID)
	}()
	go a.Invoke(ctx, &engine.Request{Path: "/first"})

	id := <-ids
	cancel()
	time.Sleep(50 * time.Millisecond)

	resp := post(t, base+invocationPfx+id+"/response", `{"statusCode":200,"body":"stale"}`)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"an answer to an invocation that is no longer current must not land on the next one")
}

// TestOneInvocationAtATime. The contract cannot express two, so the second
// caller is told rather than silently interleaved.
func TestOneInvocationAtATime(t *testing.T) {
	a, base := newAPI(t, Options{Timeout: time.Hour})

	go a.Invoke(context.Background(), &engine.Request{Path: "/first"})
	time.Sleep(50 * time.Millisecond)

	_, err := a.Invoke(context.Background(), &engine.Request{Path: "/second"})
	require.ErrorIs(t, err, ErrBusy)
	_ = base
}

// TestACallerThatGivesUpReleasesTheSlot.
func TestACallerThatGivesUpReleasesTheSlot(t *testing.T) {
	a, base := newAPI(t, Options{Timeout: time.Hour})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := a.Invoke(ctx, &engine.Request{Path: "/abandoned"})
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// And the next one gets through.
	g := &guest{t: t, base: base, handle: func([]byte) (int, string, string) {
		return 200, "second", ""
	}}
	go g.runOnce()

	resp, err := a.Invoke(context.Background(), &engine.Request{Path: "/next"})
	require.NoError(t, err)
	require.Equal(t, "second", string(resp.Body))
}

// TestABareJsonAnswerIsA200. Format 2.0 lets a handler return any JSON, and
// the platform treats it as a 200 with that JSON as the body — a handler
// returning {"hello":"world"} is extremely common and must not read as an error.
func TestABareJsonAnswerIsA200(t *testing.T) {
	a, base := newAPI(t, Options{})

	go func() {
		resp, err := http.Get(base + pathNext)
		require.NoError(t, err)
		id := resp.Header.Get(headerRequestID)
		resp.Body.Close()
		post(t, base+invocationPfx+id+"/response", `{"hello":"world"}`)
	}()

	out, err := a.Invoke(context.Background(), &engine.Request{Path: "/"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, out.Status)
	require.JSONEq(t, `{"hello":"world"}`, string(out.Body))
	require.Equal(t, []string{"application/json"}, out.Headers["Content-Type"])
}

// TestABinaryBodyRidesAsBase64BothWays. Putting raw bytes in a JSON string
// either fails to encode or silently replaces them, and a handler receiving
// mangled binary has no way to tell.
func TestABinaryBodyRidesAsBase64BothWays(t *testing.T) {
	a, base := newAPI(t, Options{})
	binary := []byte{0xff, 0xfe, 0x00, 0x01, 0x80}

	go func() {
		resp, err := http.Get(base + pathNext)
		require.NoError(t, err)
		id := resp.Header.Get(headerRequestID)
		ev, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var e event
		require.NoError(t, json.Unmarshal(ev, &e))
		require.True(t, e.IsBase64Encoded, "a non-utf8 body must be flagged")
		got, err := base64.StdEncoding.DecodeString(e.Body)
		require.NoError(t, err)
		require.Equal(t, binary, got)

		out, _ := json.Marshal(lambdaResponse{
			StatusCode:      200,
			Body:            base64.StdEncoding.EncodeToString(binary),
			IsBase64Encoded: true,
		})
		post(t, base+invocationPfx+id+"/response", string(out))
	}()

	resp, err := a.Invoke(context.Background(), &engine.Request{Path: "/", Body: binary})
	require.NoError(t, err)
	require.Equal(t, binary, resp.Body)
}

func TestAHandlerThatLiesAboutBase64IsAnError(t *testing.T) {
	a, base := newAPI(t, Options{})

	go func() {
		resp, err := http.Get(base + pathNext)
		require.NoError(t, err)
		id := resp.Header.Get(headerRequestID)
		resp.Body.Close()
		post(t, base+invocationPfx+id+"/response",
			`{"statusCode":200,"body":"not base64 at all!!","isBase64Encoded":true}`)
	}()

	_, err := a.Invoke(context.Background(), &engine.Request{Path: "/"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "base64")
}

func TestResponseHeadersComeBack(t *testing.T) {
	a, base := newAPI(t, Options{})

	go func() {
		resp, _ := http.Get(base + pathNext)
		id := resp.Header.Get(headerRequestID)
		resp.Body.Close()
		out, _ := json.Marshal(lambdaResponse{
			StatusCode: 302,
			Headers:    map[string]string{"Location": "/elsewhere"},
		})
		post(t, base+invocationPfx+id+"/response", string(out))
	}()

	resp, err := a.Invoke(context.Background(), &engine.Request{Path: "/"})
	require.NoError(t, err)
	require.Equal(t, 302, resp.Status)
	require.Equal(t, []string{"/elsewhere"}, resp.Headers["Location"])
}

// TestAnEmptyRequestIsStillAValidEvent — a GET / with no body is the common
// case and must not produce a malformed event.
func TestAnEmptyRequestIsStillAValidEvent(t *testing.T) {
	a, base := newAPI(t, Options{})

	go func() {
		resp, _ := http.Get(base + pathNext)
		id := resp.Header.Get(headerRequestID)
		ev, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var e event
		require.NoError(t, json.Unmarshal(ev, &e))
		require.Equal(t, "GET", e.RequestContext.HTTP.Method)
		require.Equal(t, "/", e.RawPath)
		require.Empty(t, e.Body)

		out, _ := json.Marshal(lambdaResponse{StatusCode: 204})
		post(t, base+invocationPfx+id+"/response", string(out))
	}()

	resp, err := a.Invoke(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, 204, resp.Status)
}

func TestTheRuntimeApiRefusesWrongMethodsAndPaths(t *testing.T) {
	_, base := newAPI(t, Options{})

	resp := post(t, base+pathNext, "")
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)

	r, err := http.Get(base + pathInitError)
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, r.StatusCode)

	r, err = http.Get(base + invocationPfx + "abc/response")
	require.NoError(t, err)
	r.Body.Close()
	require.Equal(t, http.StatusMethodNotAllowed, r.StatusCode)

	resp = post(t, base+invocationPfx+"noslash", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// With nothing in flight the id is checked first, so an unknown id wins
	// over an unknown action. That ordering is deliberate: "who are you
	// answering for" is the question that matters.
	resp = post(t, base+invocationPfx+"abc/somethingelse", "")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestAnUnknownActionOnALiveInvocationIsNotFound — the other side of that
// ordering, with a real id so the action check is the one that fires.
func TestAnUnknownActionOnALiveInvocationIsNotFound(t *testing.T) {
	a, base := newAPI(t, Options{Timeout: time.Hour})

	ids := make(chan string, 1)
	go func() {
		resp, err := http.Get(base + pathNext)
		require.NoError(t, err)
		defer resp.Body.Close()
		ids <- resp.Header.Get(headerRequestID)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Invoke(ctx, &engine.Request{Path: "/"})

	id := <-ids
	resp := post(t, base+invocationPfx+id+"/somethingelse", "")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDefaultsAreApplied(t *testing.T) {
	a := New(Options{})
	require.Equal(t, defaultTimeout, a.opts.Timeout)
	require.NotEmpty(t, a.opts.FunctionARN)
	require.NotNil(t, a.opts.Now)

	custom := New(Options{FunctionARN: "arn:custom", Timeout: time.Minute})
	require.Equal(t, "arn:custom", custom.opts.FunctionARN)
	require.Equal(t, time.Minute, custom.opts.Timeout)
}

func TestTheDeadlineHeaderMatchesTheTimeout(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	a, base := newAPI(t, Options{Timeout: 10 * time.Second, Now: func() time.Time { return at }})

	go a.Invoke(context.Background(), &engine.Request{Path: "/"})

	resp, err := http.Get(base + pathNext)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "1700000010000", resp.Header.Get(headerDeadline),
		"a handler that reads the deadline and one that is cut off should agree")
}
