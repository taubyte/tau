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

// lambda.go makes an AWS Lambda container image run here unmodified.
//
// # Why this exists at all
//
// A Lambda container image does not listen for a request. It embeds AWS's
// runtime interface client, which POLLS an endpoint for work — so on a tier
// that waits for something to listen, it starts, answers nothing, and times
// out. That is the one import this platform could not finish, and it is the
// reason the container engine exists as a serverless tier in the first place.
//
// # It needs no binary from AWS
//
// The Lambda Runtime API is a documented HTTP protocol, and the client reads
// the address to poll out of AWS_LAMBDA_RUNTIME_API. So this tier SERVES that
// protocol itself: three endpoints, on a loopback port, per instance. Nothing
// is downloaded, nothing is injected into the image, and the image is byte for
// byte the one that ran on Lambda.
//
//	GET  /2018-06-01/runtime/invocation/next            hand it the next event
//	POST /2018-06-01/runtime/invocation/{id}/response   take the result
//	POST /2018-06-01/runtime/invocation/{id}/error      take the failure
//	POST /2018-06-01/runtime/init/error                 it died starting up
//
// # It is always on, and that is what makes it no-config
//
// Which of the two shapes an image is cannot be known before it runs: a
// Dockerfile that happens to call itself a Lambda is not a signal, and asking
// an author to declare it would be asking them to describe an image they did
// not build. So the address is always in the environment and the listener is
// always bound; a container that ignores an unknown variable is unaffected, and
// whichever signal arrives first — the container listening, or the client
// polling — decides how requests reach it.
//
// # The event is API Gateway v2
//
// A handler is called with an EVENT and not an HTTP request, so an HTTP request
// has to be rendered as one. Payload format 2.0 is what a Lambda Function URL
// delivers, which is the shape a function reached over HTTP was most likely
// written against — and the response half of the same format is what it returns.
// A handler that answers with something else is treated as having returned a
// body, which is what a Function URL does too.

// The Runtime API's own paths. They carry the date because AWS versions them
// that way, and an image built against a different one would not poll here.
const (
	runtimeAPIRoot = "/2018-06-01/runtime/"
	nextPath       = runtimeAPIRoot + "invocation/next"
	invocationPath = runtimeAPIRoot + "invocation/"
	initErrorPath  = runtimeAPIRoot + "init/error"
)

// requestIDHeader is what the client reads to know which invocation it is
// answering. Everything else the protocol defines — deadline, ARN, trace id —
// is optional, and inventing an ARN for a function that is not on AWS would be
// putting a lie in the environment.
const requestIDHeader = "Lambda-Runtime-Aws-Request-Id"

var (
	// ErrLambdaFailed is a handler that answered with an error rather than a
	// result. It carries the handler's own message, which is what an author
	// needs and what a stack trace in a container log does not give them.
	ErrLambdaFailed = errors.New("container: the lambda handler failed")

	// ErrLambdaGone is an instance whose client stopped polling.
	ErrLambdaGone = errors.New("container: the lambda runtime client is not polling")
)

// lambdaAPI is one instance's Runtime API.
//
// ONE INVOCATION AT A TIME, which is not a simplification: a Lambda runtime
// client polls for the next event only after answering the previous one, so the
// protocol itself is serial. Concurrency above one is more INSTANCES, which is
// what invoke.Spec.Concurrency already means and what the pool already does.
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
		// AN ANSWER TO AN INVOCATION NOBODY IS WAITING ON. A client that
		// retried, or one answering after the caller's context ended. It is
		// accepted and dropped rather than erroring: the protocol has no way
		// for the client to do anything useful with a refusal.
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

// ── the event, and the answer ────────────────────────────────────────────────

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

	return json.Marshal(map[string]any{
		"version":         "2.0",
		"rawPath":         raw,
		"rawQueryString":  query,
		"headers":         headers,
		"body":            body,
		"isBase64Encoded": encoded,
		"requestContext": map[string]any{
			"requestId": id,
			"http": map[string]any{
				"method":   method,
				"path":     raw,
				"protocol": "HTTP/1.1",
			},
		},
	})
}

// lambdaResponse reads what the handler returned.
//
// A HANDLER THAT ANSWERED WITH SOMETHING ELSE HAS ANSWERED WITH A BODY, which
// is what a Function URL does too — a handler returning a bare string or a
// plain object is a 200 whose body is that value, and treating it as a
// malformed proxy response would break every handler that was never written
// for one.
func lambdaResponse(raw []byte) *invoke.Response {
	res := &invoke.Response{Status: http.StatusOK, Body: raw}
	if len(raw) == 0 {
		return res
	}

	var shaped struct {
		StatusCode      int                 `json:"statusCode"`
		Headers         map[string]string   `json:"headers"`
		MultiValue      map[string][]string `json:"multiValueHeaders"`
		Body            string              `json:"body"`
		IsBase64Encoded bool                `json:"isBase64Encoded"`
	}
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

// requestID is what this invocation is called on the wire. It is derived from
// the clock and a counter rather than being a UUID: nothing outside this
// instance ever sees it, and the protocol only needs the response to name the
// invocation it answers.
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
