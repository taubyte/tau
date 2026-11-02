package container

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/taubyte/tau/pkg/invoke"
)

// lambda.go runs an AWS Lambda container image unmodified. Such an image polls an
// endpoint for work instead of listening, so this tier serves the documented
// Lambda Runtime API itself, on a loopback port per instance:
//
//	GET  /2018-06-01/runtime/invocation/next            hand it the next event
//	POST /2018-06-01/runtime/invocation/{id}/response   take the result
//	POST /2018-06-01/runtime/invocation/{id}/error      take the failure
//	POST /2018-06-01/runtime/init/error                 it died starting up
//
// It is always on: whichever arrives first, a listener or a poll, decides how
// requests reach the container. Requests are rendered as API Gateway payload 2.0
// events, what a Function URL delivers.

// The Runtime API's own paths. They carry the date because AWS versions them
// that way, and an image built against a different one would not poll here.
const (
	runtimeAPIRoot = "/2018-06-01/runtime/"
	nextPath       = runtimeAPIRoot + "invocation/next"
	invocationPath = runtimeAPIRoot + "invocation/"
	initErrorPath  = runtimeAPIRoot + "init/error"
)

// requestIDHeader tells the client which invocation it answers; the protocol's
// optional fields are left out rather than faked.
const requestIDHeader = "Lambda-Runtime-Aws-Request-Id"

var (
	// ErrLambdaFailed is a handler that answered with an error rather than a
	// result. It carries the handler's own message, which is what an author
	// needs and what a stack trace in a container log does not give them.
	ErrLambdaFailed = errors.New("container: the lambda handler failed")

	// ErrLambdaGone is an instance whose client stopped polling.
	ErrLambdaGone = errors.New("container: the lambda runtime client is not polling")
)

// lambdaAPI is one instance's Runtime API, one invocation at a time as the protocol
// is serial; concurrency is more instances.
type lambdaAPI struct {
	listener net.Listener
	server   *http.Server

	// work carries one pending invocation to whichever poll arrives next.
	work chan *lambdaCall

	// polled closes on the first poll, which is this mode's readiness signal.
	polledOnce sync.Once
	polled     chan struct{}

	mu      sync.Mutex
	current *lambdaCall
	closed  bool
}

type lambdaCall struct {
	id    string
	event []byte

	done   chan struct{}
	result []byte
	err    error
}

func newLambdaAPI() (*lambdaAPI, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("container: binding the lambda runtime api: %w", err)
	}

	api := &lambdaAPI{
		listener: listener,
		work:     make(chan *lambdaCall),
		polled:   make(chan struct{}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+nextPath, api.next)
	mux.HandleFunc("POST "+invocationPath+"{id}/response", api.response)
	mux.HandleFunc("POST "+invocationPath+"{id}/error", api.failed)
	mux.HandleFunc("POST "+initErrorPath, api.initError)

	api.server = &http.Server{Handler: mux}
	go api.server.Serve(listener)
	return api, nil
}

// address is what goes in AWS_LAMBDA_RUNTIME_API.
func (a *lambdaAPI) address() string { return a.listener.Addr().String() }

// next blocks until there is work, which is exactly what the protocol says: the
// client long-polls and the platform answers when it has something.
func (a *lambdaAPI) next(w http.ResponseWriter, r *http.Request) {
	a.polledOnce.Do(func() { close(a.polled) })

	select {
	case call := <-a.work:
		a.mu.Lock()
		a.current = call
		a.mu.Unlock()

		w.Header().Set(requestIDHeader, call.id)
		w.Header().Set("Content-Type", "application/json")
		w.Write(call.event)
	case <-r.Context().Done():
		// The client gave up or the instance is closing. No body: there was no
		// invocation, so there is nothing to answer with.
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *lambdaAPI) response(w http.ResponseWriter, r *http.Request) {
	a.finish(w, r, nil)
}

func (a *lambdaAPI) failed(w http.ResponseWriter, r *http.Request) {
	a.finish(w, r, ErrLambdaFailed)
}

// finish hands the body back to whoever is waiting on this invocation.
func (a *lambdaAPI) finish(w http.ResponseWriter, r *http.Request, failure error) {
	body, err := readAllLimited(r)
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}

	a.mu.Lock()
	call := a.current
	if call != nil && call.id == r.PathValue("id") {
		a.current = nil
	} else {
		call = nil
	}
	a.mu.Unlock()

	if call == nil {
		// An answer nobody waits on is accepted and dropped; the client could do nothing
		// with a refusal.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	call.result = body
	if failure != nil {
		call.err = fmt.Errorf("%w: %s", failure, strings.TrimSpace(string(body)))
	}
	close(call.done)
	w.WriteHeader(http.StatusAccepted)
}

// initError is the client saying it could not start. There is no invocation to
// fail, so it is recorded for whoever asks next.
func (a *lambdaAPI) initError(w http.ResponseWriter, r *http.Request) {
	body, _ := readAllLimited(r)

	a.mu.Lock()
	if a.current != nil {
		a.current.err = fmt.Errorf("%w during initialisation: %s",
			ErrLambdaFailed, strings.TrimSpace(string(body)))
		close(a.current.done)
		a.current = nil
	}
	a.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}

// invoke hands one event to the client and waits for its answer.
func (a *lambdaAPI) invoke(ctx context.Context, id string, event []byte) ([]byte, error) {
	call := &lambdaCall{id: id, event: event, done: make(chan struct{})}

	select {
	case a.work <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case <-call.done:
		return call.result, call.err
	case <-ctx.Done():
		// THE INVOCATION IS ABANDONED AND THE CLIENT IS NOT. It will answer
		// eventually and finish() drops it, which is better than holding a
		// goroutine for a caller that has gone.
		return nil, ctx.Err()
	}
}

func (a *lambdaAPI) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	return a.server.Close()
}

// lambdaEvent renders an invocation as API Gateway payload format 2.0.
func lambdaEvent(req *invoke.Request, id string) ([]byte, error) {
	path := req.Path
	if path == "" {
		path = "/"
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	raw, query := path, ""
	if i := strings.IndexByte(path, '?'); i >= 0 {
		raw, query = path[:i], path[i+1:]
	}

	// A HEADER MAY REPEAT AND THIS FORMAT'S MAP MAY NOT, so repeats are joined
	// with a comma — which is what the format itself specifies rather than
	// something chosen here.
	headers := map[string]string{}
	for name, values := range req.Headers {
		headers[strings.ToLower(name)] = strings.Join(values, ",")
	}

	// A BODY THAT IS NOT TEXT IS BASE64, and the flag says which — otherwise a
	// JPEG would be mangled by being put in a JSON string.
	body, encoded := "", false
	if len(req.Body) > 0 {
		if utf8.Valid(req.Body) {
			body = string(req.Body)
		} else {
			body, encoded = base64.StdEncoding.EncodeToString(req.Body), true
		}
	}

	return json.Marshal(lambdaPayload{
		Body:            body,
		Headers:         headers,
		IsBase64Encoded: encoded,
		RawPath:         raw,
		RawQueryString:  query,
		RequestContext: lambdaContext{
			HTTP:      lambdaHTTP{Method: method, Path: raw, Protocol: "HTTP/1.1"},
			RequestID: id,
		},
		Version: "2.0",
	})
}

// The event's fields are declared in sorted order, the order encoding/json writes a
// map's keys in, so the bytes are what a map would have produced.
type lambdaPayload struct {
	Body            string            `json:"body"`
	Headers         map[string]string `json:"headers"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
	RawPath         string            `json:"rawPath"`
	RawQueryString  string            `json:"rawQueryString"`
	RequestContext  lambdaContext     `json:"requestContext"`
	Version         string            `json:"version"`
}

type lambdaContext struct {
	HTTP      lambdaHTTP `json:"http"`
	RequestID string     `json:"requestId"`
}

type lambdaHTTP struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
}

// lambdaResponse reads what the handler returned; anything not a proxy response is
// a 200 with that body, as a Function URL treats it.
func lambdaResponse(raw []byte) *invoke.Response {
	res := &invoke.Response{Status: http.StatusOK, Body: raw}
	if len(raw) == 0 {
		return res
	}

	type lambdaResponse struct {
		StatusCode      int                 `json:"statusCode"`
		Headers         map[string]string   `json:"headers"`
		MultiValue      map[string][]string `json:"multiValueHeaders"`
		Body            string              `json:"body"`
		IsBase64Encoded bool                `json:"isBase64Encoded"`
	}
	var shaped lambdaResponse
	if err := json.Unmarshal(raw, &shaped); err != nil || shaped.StatusCode == 0 {
		return res
	}

	res.Status = shaped.StatusCode
	res.Headers = map[string][]string{}
	for name, value := range shaped.Headers {
		res.Headers[name] = []string{value}
	}
	for name, values := range shaped.MultiValue {
		res.Headers[name] = values
	}

	res.Body = []byte(shaped.Body)
	if shaped.IsBase64Encoded {
		if decoded, err := base64.StdEncoding.DecodeString(shaped.Body); err == nil {
			res.Body = decoded
		}
	}
	return res
}

// maxLambdaResponse bounds what a handler may answer with before it is read.
// AWS's own limit is 6MB for a synchronous invocation; this is that, and it
// exists because the body is read into memory on a node shared with other
// tenants.
const maxLambdaResponse = 6 << 20

// readAllLimited reads a body, bounded, so a handler cannot hand this node an
// unbounded one.
func readAllLimited(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, maxLambdaResponse))
}

// The invocation id comes from a counter; only this instance ever sees it.
var lambdaCounter atomic

type atomic struct {
	mu sync.Mutex
	n  uint64
}

func (a *atomic) next() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n++
	return a.n
}

func requestID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + strconv.FormatUint(lambdaCounter.next(), 36)
}
