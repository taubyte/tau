// Package lambda speaks the AWS Lambda Runtime API, so a Lambda image can be
// imported and run unchanged.
//
// # Why this has to exist
//
// Every other engine tier is handed a request. A Lambda image is not: it runs a
// loop that POLLS an HTTP endpoint for work, executes the handler, and posts
// the result back. The endpoint is the platform's, not the image's, and it is
// the whole of the contract between them:
//
//	GET  /2018-06-01/runtime/invocation/next          block until there is work
//	POST /2018-06-01/runtime/invocation/{id}/response the answer
//	POST /2018-06-01/runtime/invocation/{id}/error    a handled failure
//	POST /2018-06-01/runtime/init/error               it never got started
//
// A platform that does not serve those four does not run Lambda images at all,
// however good its container support is. That is why this is a package and not
// a footnote: it is the entire difference between "we support containers" and
// "bring your Lambda".
//
// # The shape of the contract, and the two things that are easy to get wrong
//
// NEXT BLOCKS, AND THAT IS NOT A POLLING LOOP TO OPTIMISE AWAY. The guest calls
// next and the connection stays open until there is work. Returning 204 or an
// empty body to say "nothing yet" makes a well-behaved runtime spin, and the
// AWS runtimes treat a non-200 from next as fatal. So a waiting guest waits.
//
// ONE INVOCATION AT A TIME PER RUNTIME. The contract has no way to express two
// concurrent invocations to one guest — there is a single next endpoint and the
// response is matched by an id the guest was handed. That is the same shape as
// engine.Spec's Concurrency of 1, and it is not a coincidence: the Lambda model
// IS one-at-a-time, and it is why that is the pool's default.
//
// # What is deliberately not here
//
// No AWS credentials, no X-Ray tracing header, no /2020-01-01/extension API.
// Each is a thing a Lambda image MAY use and none is required to run one, so
// each would be surface built ahead of a caller that wanted it.
package lambda

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/taubyte/tau/pkg/v2/engine"
)

// Runtime API paths, spelled once.
const (
	pathNext      = "/2018-06-01/runtime/invocation/next"
	pathInitError = "/2018-06-01/runtime/init/error"
	invocationPfx = "/2018-06-01/runtime/invocation/"

	headerRequestID = "Lambda-Runtime-Aws-Request-Id"
	headerDeadline  = "Lambda-Runtime-Deadline-Ms"
	headerARN       = "Lambda-Runtime-Invoked-Function-Arn"
)

var (
	ErrBusy         = errors.New("lambda: an invocation is already in flight")
	ErrGuestFailed  = errors.New("lambda: the handler reported an error")
	ErrInitFailed   = errors.New("lambda: the runtime failed to initialise")
	ErrNoInvocation = errors.New("lambda: no such invocation")
)

// Options configure an API.
type Options struct {
	// FunctionARN is what the guest is told it is running as. Some runtimes
	// log it; none require a real one.
	FunctionARN string

	// Timeout is how long a guest has to answer before the invocation is
	// abandoned. It is also what the deadline header advertises, because a
	// handler that reads the deadline and one that is cut off should agree.
	Timeout time.Duration

	// Now is the local clock. Tests inject one.
	Now func() time.Time
}

const defaultTimeout = 30 * time.Second

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.FunctionARN == "" {
		o.FunctionARN = "arn:tau:lambda:local:0:function:imported"
	}
	return o
}

// API is one guest's runtime endpoint: a queue of one, and the four routes.
type API struct {
	opts Options

	mu      sync.Mutex
	current *invocation
	initErr error

	// ready carries the pending invocation to whichever `next` call is
	// waiting, or is waited on by the next one to arrive.
	ready chan *invocation
}

type invocation struct {
	id       string
	event    []byte
	deadline time.Time

	done   chan struct{}
	once   sync.Once
	result *engine.Response
	err    error
}

func (i *invocation) finish(resp *engine.Response, err error) {
	i.once.Do(func() {
		i.result, i.err = resp, err
		close(i.done)
	})
}

// New builds an API.
func New(opts Options) *API {
	return &API{opts: opts.withDefaults(), ready: make(chan *invocation, 1)}
}

// Invoke hands one request to the guest and waits for its answer.
//
// It is the engine.Instance side of the contract: everything above translates
// between this call and the four HTTP routes below.
func (a *API) Invoke(ctx contextLike, req *engine.Request) (*engine.Response, error) {
	a.mu.Lock()
	if a.initErr != nil {
		err := a.initErr
		a.mu.Unlock()
		return nil, err
	}
	if a.current != nil {
		a.mu.Unlock()
		return nil, ErrBusy
	}
	inv := &invocation{
		id:       newRequestID(a.opts.Now()),
		deadline: a.opts.Now().Add(a.opts.Timeout),
		done:     make(chan struct{}),
	}
	event, err := encodeEvent(req)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	inv.event = event
	a.current = inv
	a.mu.Unlock()

	// Hand it to a waiting `next`, or park it for the next one to arrive.
	select {
	case a.ready <- inv:
	default:
	}

	defer func() {
		a.mu.Lock()
		if a.current == inv {
			a.current = nil
		}
		a.mu.Unlock()
	}()

	select {
	case <-inv.done:
		return inv.result, inv.err
	case <-ctx.Done():
		inv.finish(nil, ctx.Err())
		return nil, ctx.Err()
	}
}

// contextLike is the slice of context.Context used here, named so this file
// does not import context solely for one method set.
type contextLike interface {
	Done() <-chan struct{}
	Err() error
}

// Handler is the runtime API, to be served on the address the guest's
// AWS_LAMBDA_RUNTIME_API environment variable points at.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(pathNext, a.handleNext)
	mux.HandleFunc(pathInitError, a.handleInitError)
	mux.HandleFunc(invocationPfx, a.handleInvocation)
	return mux
}

// handleNext blocks until there is work. See the package doc: a non-200 or an
// empty answer here makes a conforming runtime spin or die.
func (a *API) handleNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "next is a GET", http.StatusMethodNotAllowed)
		return
	}

	a.mu.Lock()
	inv := a.current
	a.mu.Unlock()

	if inv == nil || inv.isDone() {
		select {
		case inv = <-a.ready:
		case <-r.Context().Done():
			return
		}
	}

	w.Header().Set(headerRequestID, inv.id)
	w.Header().Set(headerARN, a.opts.FunctionARN)
	w.Header().Set(headerDeadline, fmt.Sprint(inv.deadline.UnixMilli()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(inv.event)
}

// handleInvocation routes /{id}/response and /{id}/error.
func (a *API) handleInvocation(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, invocationPfx)
	id, action, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		http.Error(w, "not a runtime invocation path", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "these are POSTs", http.StatusMethodNotAllowed)
		return
	}

	a.mu.Lock()
	inv := a.current
	a.mu.Unlock()

	// AN ANSWER TO AN INVOCATION THAT IS NO LONGER CURRENT IS REFUSED. A guest
	// that timed out and then answered would otherwise deliver the previous
	// request's body as the answer to the next one, which is the worst possible
	// version of this bug: a response that is well-formed and belongs to
	// somebody else.
	if inv == nil || inv.id != id {
		http.Error(w, ErrNoInvocation.Error(), http.StatusBadRequest)
		return
	}

	body, err := readAll(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch action {
	case "response":
		resp, err := decodeResponse(body)
		inv.finish(resp, err)
	case "error":
		inv.finish(nil, fmt.Errorf("%w: %s", ErrGuestFailed, summarise(body)))
	default:
		http.Error(w, "unknown invocation action", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// handleInitError records that the runtime never came up. Every subsequent
// invocation fails immediately rather than waiting out the timeout: a guest
// that said it could not start is not going to answer.
func (a *API) handleInitError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "init/error is a POST", http.StatusMethodNotAllowed)
		return
	}
	body, _ := readAll(r)

	a.mu.Lock()
	a.initErr = fmt.Errorf("%w: %s", ErrInitFailed, summarise(body))
	inv := a.current
	a.mu.Unlock()

	if inv != nil {
		inv.finish(nil, a.initErr)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (i *invocation) isDone() bool {
	select {
	case <-i.done:
		return true
	default:
		return false
	}
}

// InitError is what the guest reported at startup, if anything.
func (a *API) InitError() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.initErr
}

// ─────────────────────── the event and the answer ───────────────────────

// event is the API Gateway HTTP API (payload format 2.0) shape, which is what
// an HTTP-triggered Lambda is written against.
//
// 2.0 RATHER THAN 1.0 because it is the current default for HTTP APIs and it is
// the simpler of the two: one `rawPath`, headers already joined, and a response
// the handler may return as a bare object. Supporting both is a thing to do
// when something needs it, not before.
type event struct {
	Version               string            `json:"version"`
	RouteKey              string            `json:"routeKey"`
	RawPath               string            `json:"rawPath"`
	RawQueryString        string            `json:"rawQueryString"`
	Headers               map[string]string `json:"headers"`
	RequestContext        requestContext    `json:"requestContext"`
	Body                  string            `json:"body,omitempty"`
	IsBase64Encoded       bool              `json:"isBase64Encoded"`
	StageVariablesUnused  struct{}          `json:"-"`
	PathParametersUnused  struct{}          `json:"-"`
	QueryStringParameters map[string]string `json:"queryStringParameters,omitempty"`
}

type requestContext struct {
	HTTP requestContextHTTP `json:"http"`
}

type requestContextHTTP struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

func encodeEvent(req *engine.Request) ([]byte, error) {
	if req == nil {
		req = &engine.Request{}
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	rawPath, rawQuery, _ := strings.Cut(path, "?")

	// Headers are joined with commas, which is what payload format 2.0 does.
	headers := make(map[string]string, len(req.Headers))
	for k, vs := range req.Headers {
		headers[strings.ToLower(k)] = strings.Join(vs, ",")
	}

	e := event{
		Version:        "2.0",
		RouteKey:       "$default",
		RawPath:        rawPath,
		RawQueryString: rawQuery,
		Headers:        headers,
		RequestContext: requestContext{HTTP: requestContextHTTP{Method: method, Path: rawPath}},
	}

	// A BODY THAT IS NOT VALID UTF-8 IS BASE64, and the flag says so. Putting
	// raw bytes in a JSON string would either fail to encode or silently
	// replace them, and a handler receiving mangled binary has no way to tell.
	if len(req.Body) > 0 {
		if utf8.Valid(req.Body) {
			e.Body = string(req.Body)
		} else {
			e.Body = base64.StdEncoding.EncodeToString(req.Body)
			e.IsBase64Encoded = true
		}
	}
	return json.Marshal(e)
}

// lambdaResponse is what a handler returns under payload format 2.0.
type lambdaResponse struct {
	StatusCode      int               `json:"statusCode"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	IsBase64Encoded bool              `json:"isBase64Encoded"`
}

func decodeResponse(body []byte) (*engine.Response, error) {
	var lr lambdaResponse
	if err := json.Unmarshal(body, &lr); err != nil || lr.StatusCode == 0 {
		// FORMAT 2.0 LETS A HANDLER RETURN ANY JSON, and then the platform
		// treats it as a 200 with that JSON as the body. Guessing "this did not
		// parse as a response envelope, so it must be a payload" is what AWS
		// does, and a handler returning {"hello":"world"} is a very common
		// thing that must not read as an error.
		return &engine.Response{
			Status:  http.StatusOK,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    body,
		}, nil
	}

	out := &engine.Response{Status: lr.StatusCode, Headers: map[string][]string{}}
	for k, v := range lr.Headers {
		out.Headers[k] = []string{v}
	}
	if lr.IsBase64Encoded {
		raw, err := base64.StdEncoding.DecodeString(lr.Body)
		if err != nil {
			return nil, fmt.Errorf("lambda: handler said base64 and sent something else: %w", err)
		}
		out.Body = raw
	} else {
		out.Body = []byte(lr.Body)
	}
	return out, nil
}

func summarise(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "no detail given"
	}
	const max = 512
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func newRequestID(now time.Time) string {
	return fmt.Sprintf("%016x", now.UnixNano())
}

// readAll reads a request body with a ceiling. A guest is not a stranger, but
// it is a process that can be wrong, and an unbounded read of its answer is an
// unbounded allocation driven by something that already misbehaved.
func readAll(r *http.Request) ([]byte, error) {
	const maxBody = 8 << 20
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxBody))
}
