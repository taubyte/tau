package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
)

// A Lambda runtime client implemented from the protocol's description, not from
// this package, so an AWS-built image is shown to drive it.

// runtimeClient is a Lambda runtime interface client, in miniature: it polls
// /next forever and answers each event with what handle returns.
type runtimeClient struct {
	address string
	handle  func(event []byte) (result []byte, failed bool)

	// ctx cancels the long poll, or cleanup deadlocks: /next blocks until the server
	// itself ends.
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	events [][]byte
	done   chan struct{}
}

func startClient(t *testing.T, address string, handle func([]byte) ([]byte, bool)) *runtimeClient {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	c := &runtimeClient{
		address: address, handle: handle,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	go c.loop()
	t.Cleanup(func() {
		cancel()
		<-c.done
	})
	return c
}

func (c *runtimeClient) loop() {
	defer close(c.done)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, "http://"+c.address+nextPath, nil)
		if err != nil {
			return
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}

		id := res.Header.Get(requestIDHeader)
		event, _ := io.ReadAll(res.Body)
		res.Body.Close()

		if id == "" {
			// A 204: no invocation. The real client would poll again.
			continue
		}

		c.mu.Lock()
		c.events = append(c.events, event)
		c.mu.Unlock()

		result, failed := c.handle(event)
		route := "response"
		if failed {
			route = "error"
		}
		post, err := http.NewRequestWithContext(c.ctx, http.MethodPost,
			"http://"+c.address+invocationPath+id+"/"+route, strings.NewReader(string(result)))
		if err != nil {
			return
		}
		post.Header.Set("Content-Type", "application/json")
		answer, err := http.DefaultClient.Do(post)
		if err != nil {
			return
		}
		answer.Body.Close()
	}
}

func (c *runtimeClient) seen() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.events...)
}

func echoHandler(event []byte) ([]byte, bool) { return event, false }

func newAPI(t *testing.T) *lambdaAPI {
	t.Helper()
	api, err := newLambdaAPI()
	require.NoError(t, err)
	t.Cleanup(func() { api.Close() })
	return api
}

func TestTheRuntimeAPIDriversAClient(t *testing.T) {
	api := newAPI(t)
	startClient(t, api.address(), echoHandler)

	result, err := api.invoke(context.Background(), "req-1", []byte(`{"hello":"world"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"world"}`, string(result))
}

func TestPollingIsWhatSaysItIsReady(t *testing.T) {
	api := newAPI(t)

	select {
	case <-api.polled:
		t.Fatal("nothing has polled yet")
	default:
	}

	startClient(t, api.address(), echoHandler)
	select {
	case <-api.polled:
	case <-time.After(5 * time.Second):
		t.Fatal("a poll never registered")
	}
}

func TestAFailedInvocationCarriesTheHandlersMessage(t *testing.T) {
	api := newAPI(t)
	startClient(t, api.address(), func([]byte) ([]byte, bool) {
		return []byte(`{"errorMessage":"table not found"}`), true
	})

	_, err := api.invoke(context.Background(), "req-1", []byte(`{}`))
	require.ErrorIs(t, err, ErrLambdaFailed)
	require.ErrorContains(t, err, "table not found")
}

func TestTwoInvocationsRunOneAfterTheOther(t *testing.T) {
	api := newAPI(t)

	var inFlight, peak int
	var mu sync.Mutex
	startClient(t, api.address(), func(event []byte) ([]byte, bool) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return event, false
	})

	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := api.invoke(context.Background(), fmt.Sprintf("req-%d", i), []byte(`{}`))
			require.NoError(t, err)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, peak, "the protocol is serial")
}

func TestACancelledInvocationIsDropped(t *testing.T) {
	api := newAPI(t)

	release := make(chan struct{})
	startClient(t, api.address(), func(event []byte) ([]byte, bool) {
		<-release
		return event, false
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := api.invoke(ctx, "req-1", []byte(`{}`))
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(release)

	// And the next one still works, which is what proves the dropped answer
	// did not wedge the loop.
	result, err := api.invoke(context.Background(), "req-2", []byte(`{"second":true}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"second":true}`, string(result))
}

func TestAPollWithNoWorkWaits(t *testing.T) {
	api := newAPI(t)

	answered := make(chan string, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://"+api.address()+nextPath, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		answered <- string(body)
	}()

	select {
	case got := <-answered:
		t.Fatalf("it answered a poll with no work: %q", got)
	case <-time.After(200 * time.Millisecond):
	}

	// And it is released when there is work.
	go api.invoke(context.Background(), "req-1", []byte(`{"now":true}`))
	select {
	case got := <-answered:
		require.JSONEq(t, `{"now":true}`, got)
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting poll never got the work")
	}
}

func TestTheEventIsApiGatewayV2(t *testing.T) {
	raw, err := lambdaEvent(&invoke.Request{
		Method:  http.MethodPost,
		Path:    "/orders?limit=10",
		Headers: map[string][]string{"X-Trace": {"abc"}, "Accept": {"a", "b"}},
		Body:    []byte(`{"id":1}`),
	}, "req-1")
	require.NoError(t, err)

	var event map[string]any
	require.NoError(t, json.Unmarshal(raw, &event))

	require.Equal(t, "2.0", event["version"])
	require.Equal(t, "/orders", event["rawPath"], "the query is its own field")
	require.Equal(t, "limit=10", event["rawQueryString"])
	require.Equal(t, `{"id":1}`, event["body"])
	require.Equal(t, false, event["isBase64Encoded"])

	headers := event["headers"].(map[string]any)
	require.Equal(t, "abc", headers["x-trace"], "lower-cased")
	require.Equal(t, "a,b", headers["accept"], "a repeat is joined, which is what the format says")

	http := event["requestContext"].(map[string]any)["http"].(map[string]any)
	require.Equal(t, "POST", http["method"])
	require.Equal(t, "/orders", http["path"])
}

func TestANonTextBodyIsEncoded(t *testing.T) {
	body := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00}

	raw, err := lambdaEvent(&invoke.Request{Body: body}, "req-1")
	require.NoError(t, err)

	var event map[string]any
	require.NoError(t, json.Unmarshal(raw, &event))
	require.Equal(t, true, event["isBase64Encoded"])

	decoded, err := base64.StdEncoding.DecodeString(event["body"].(string))
	require.NoError(t, err)
	require.Equal(t, body, decoded)
}

func TestAProxyShapedAnswerIsRead(t *testing.T) {
	res := lambdaResponse([]byte(`{
		"statusCode": 201,
		"headers": {"content-type": "application/json"},
		"multiValueHeaders": {"set-cookie": ["a=1", "b=2"]},
		"body": "{\"ok\":true}"
	}`))

	require.Equal(t, 201, res.Status)
	require.Equal(t, []string{"application/json"}, res.Headers["content-type"])
	require.Equal(t, []string{"a=1", "b=2"}, res.Headers["set-cookie"], "a cookie may repeat")
	require.Equal(t, `{"ok":true}`, string(res.Body))
}

func TestAnUnshapedAnswerIsABody(t *testing.T) {
	for _, raw := range []string{`"just a string"`, `{"id":1}`, `[1,2,3]`, `not json at all`} {
		res := lambdaResponse([]byte(raw))
		require.Equal(t, 200, res.Status, raw)
		require.Equal(t, raw, string(res.Body), raw)
	}

	empty := lambdaResponse(nil)
	require.Equal(t, 200, empty.Status)
	require.Empty(t, empty.Body)
}

func TestABase64ProxyBodyIsDecoded(t *testing.T) {
	res := lambdaResponse([]byte(`{"statusCode":200,"body":"aGVsbG8=","isBase64Encoded":true}`))
	require.Equal(t, "hello", string(res.Body))
}

func TestRequestIdsDoNotRepeat(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := requestID()
		require.False(t, seen[id], "repeated: %s", id)
		seen[id] = true
	}
}

// envOf reads one variable out of a container's environment, the way the image
// itself would.
func envOf(env []string, name string) string {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, name+"="); ok {
			return value
		}
	}
	return ""
}

func TestALambdaImageRunsOnThisTier(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true

	// The container listens on nothing; a tier needing a socket would time out.
	rt.listen = func(int) (closer, error) { return closerFunc(func() error { return nil }), nil }
	started := make(chan *runtimeClient, 1)

	e := newRuntime(t, rt, Image{Ref: "1234.dkr.ecr.us-east-1.amazonaws.com/app:1"}, nil)

	// Start blocks until something is ready, so the client has to come up
	// alongside it — which is what the real one does when the image starts.
	go func() {
		for range 200 {
			rt.mu.Lock()
			n := len(rt.created)
			var address string
			if n > 0 {
				address = envOf(rt.created[n-1].Env, "AWS_LAMBDA_RUNTIME_API")
			}
			rt.mu.Unlock()

			if address != "" {
				started <- startClient(t, address, func(event []byte) ([]byte, bool) {
					var e map[string]any
					json.Unmarshal(event, &e)
					return []byte(fmt.Sprintf(
						`{"statusCode":201,"headers":{"x-from":"lambda"},"body":%q}`,
						e["rawPath"])), false
				})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	inst, err := e.Start(context.Background(), invoke.Spec{
		Runtime: Name, Name: "shop/prod/service/Img", Revision: "r1",
	})
	require.NoError(t, err, "a polling image never became ready")
	defer inst.Close()

	res, err := inst.Serve(context.Background(), &invoke.Request{
		Method: http.MethodPost, Path: "/orders", Body: []byte(`{"id":1}`),
	})
	require.NoError(t, err)

	require.Equal(t, 201, res.Status, "the handler's own status")
	require.Equal(t, "/orders", string(res.Body), "it saw the path, as an event")
	require.Equal(t, []string{"lambda"}, res.Headers["x-from"])
	// The answer came from the client, so it started; the goroutine hands it over
	// after startClient returns, which can be after the client answered.
	select {
	case client := <-started:
		require.Len(t, client.seen(), 1, "the handler saw exactly one event")
	case <-time.After(5 * time.Second):
		t.Fatal("no runtime client ever started")
	}
}

func TestEveryContainerIsToldWhereTheRuntimeApiIs(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	rt.mu.Lock()
	defer rt.mu.Unlock()
	require.Len(t, rt.created, 1)
	require.NotEmpty(t, envOf(rt.created[0].Env, "AWS_LAMBDA_RUNTIME_API"))
}

func TestAListeningImageStillGoesOverHttp(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, nil)

	inst, err := e.Start(context.Background(), spec())
	require.NoError(t, err)
	defer inst.Close()

	require.False(t, inst.(*instance).polls, "nothing polled, so it is not a lambda")

	// The fake answers 418, proving the HTTP path served this, not the runtime API.
	res, err := inst.Serve(context.Background(), &invoke.Request{Method: http.MethodGet, Path: "/"})
	require.NoError(t, err)
	require.Equal(t, http.StatusTeapot, res.Status)
	require.Equal(t, []string{"/"}, res.Headers["X-Echo-Path"])
}

func TestAnImageThatNeitherListensNorPollsTimesOut(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.listen = func(int) (closer, error) { return closerFunc(func() error { return nil }), nil }

	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{WithReadyTimeout(150 * time.Millisecond), WithReadyInterval(10 * time.Millisecond)}
	})

	_, err := e.Start(context.Background(), spec())
	require.ErrorIs(t, err, ErrNotReady)
	require.ErrorContains(t, err, "nothing polled the lambda runtime api either")
}

func TestAContainerThatBindsNothingCannotBeAnsweredForByAnotherProcess(t *testing.T) {
	rt := newFakeBackend()
	rt.imagePresent = true
	rt.listen = func(int) (closer, error) { return closerFunc(func() error { return nil }), nil }

	var squat sync.Once
	var dialer net.Dialer
	e := newRuntime(t, rt, Image{Ref: "acme/shop:1"}, func() []Option {
		return []Option{
			WithReadyTimeout(150 * time.Millisecond), WithReadyInterval(10 * time.Millisecond),
			WithDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
				squat.Do(func() {
					if l, err := net.Listen("tcp", addr); err == nil {
						t.Cleanup(func() { l.Close() })
					}
				})
				return dialer.DialContext(ctx, network, addr)
			}),
		}
	})

	_, err := e.Start(context.Background(), spec())
	require.ErrorIs(t, err, ErrNotReady)
}

func TestAnInitErrorFailsThePendingInvocation(t *testing.T) {
	api := newAPI(t)

	// A client that takes the work and then reports that it could not start,
	// which is the order a real one fails in when the handler is missing.
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://"+api.address()+nextPath, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		io.ReadAll(res.Body)
		res.Body.Close()

		post, _ := http.Post("http://"+api.address()+initErrorPath, "application/json",
			strings.NewReader(`{"errorMessage":"no handler named index.handler"}`))
		if post != nil {
			post.Body.Close()
		}
	}()

	_, err := api.invoke(context.Background(), "req-1", []byte(`{}`))
	require.ErrorIs(t, err, ErrLambdaFailed)
	require.ErrorContains(t, err, "during initialisation")
	require.ErrorContains(t, err, "no handler named index.handler")
}

func TestAnAnswerForAnUnknownInvocationIsAccepted(t *testing.T) {
	api := newAPI(t)

	res, err := http.Post(
		"http://"+api.address()+invocationPath+"nobody-asked/response",
		"application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusAccepted, res.StatusCode)
}

func TestClosingTheRuntimeApiTwiceIsFine(t *testing.T) {
	api, err := newLambdaAPI()
	require.NoError(t, err)

	require.NoError(t, api.Close())
	require.NoError(t, api.Close())
}

func TestTheApiStopsAnsweringWhenItIsClosed(t *testing.T) {
	api, err := newLambdaAPI()
	require.NoError(t, err)
	address := api.address()
	require.NoError(t, api.Close())

	req, _ := http.NewRequest(http.MethodGet, "http://"+address+nextPath, nil)
	_, err = http.DefaultClient.Do(req)
	require.Error(t, err, "the listener is gone")
}

// The typed payload writes the bytes the map form wrote: encoding/json sorts a map's
// keys, and the struct declares its fields in that order.
func TestTheLambdaEventIsTheBytesTheMapFormWrote(t *testing.T) {
	for _, req := range []*invoke.Request{
		{Method: "POST", Path: "/orders/1?expand=items&x=1", Headers: map[string][]string{"X-A": {"1", "2"}, "Content-Type": {"application/json"}}, Body: []byte(`{"a":1}`)},
		{Path: "", Body: []byte{0xff, 0xfe, 0x00}},
		{Method: "GET", Path: "/"},
	} {
		got, err := lambdaEvent(req, "req-1")
		require.NoError(t, err)

		var m map[string]any
		require.NoError(t, json.Unmarshal(got, &m))
		want, err := json.Marshal(m)
		require.NoError(t, err)
		require.Equal(t, string(want), string(got))
	}
}

func BenchmarkLambdaEvent(b *testing.B) {
	req := &invoke.Request{Method: "POST", Path: "/orders/1?expand=items", Headers: map[string][]string{"Content-Type": {"application/json"}, "X-Request-Id": {"abc"}}, Body: []byte(`{"order":1,"items":[1,2,3]}`)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := lambdaEvent(req, "req-1"); err != nil {
			b.Fatal(err)
		}
	}
}
