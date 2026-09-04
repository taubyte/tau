package service

import (
	"context"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"
	"github.com/spf13/afero"
)

type Redirect string
type TemporaryRedirect Redirect
type PermanentRedirect Redirect

type Handler func(ctx Context) (interface{}, error)

// RouteAuthHandler is a route's auth block, and it has two halves because a
// token only answers one of the two questions a route has to ask. Validator
// says who is asking. Object says whether that caller may have the thing this
// route's path names — the project, the job, the cid.
//
// They are separate fields so that a route which serves something a project
// owns cannot declare the first and quietly omit the second: what a route
// authorizes is in the route table, where it can be read, and where a test can
// tell a route that names an object from one that only checks a token.
type RouteAuthHandler struct {
	// Validator authenticates the caller.
	Validator Handler

	// Object authorizes the object the path names, for the caller Validator
	// just identified. Left nil by a route whose path names nothing owned.
	Object func(Context) error

	// GC releases whatever Validator built for the request.
	GC Handler
}

// Validate is the handler the router runs for a route: Validator first, Object
// second, because there is nothing to authorize until we know who is asking.
//
// A refusal by Object calls GC here rather than leaving it to the route,
// because an auth handler that returns an error never reaches the route's
// cleanup — anything Validator built would sit out its full timeout with
// nobody left to release it.
func (h RouteAuthHandler) Validate() Handler {
	if h.Object == nil {
		return h.Validator
	}

	return func(ctx Context) (interface{}, error) {
		if h.Validator != nil {
			if _, err := h.Validator(ctx); err != nil {
				return nil, err
			}
		}

		if err := h.Object(ctx); err != nil {
			if h.GC != nil {
				h.GC(ctx)
			}
			return nil, err
		}

		return nil, nil
	}
}

type RawData struct {
	ContentType string
	Data        []byte
}

type RawStream struct {
	ContentType string
	Stream      io.ReadCloser
}

type RawRouteDefinition struct {
	Hosts       []string // scope to these hosts (see RouteDefinition.Hosts)
	Path        string
	PathPrefix  string
	Vars        Variables
	Scope       []string
	Auth        RouteAuthHandler
	Handler     Handler
	RawResponse bool
}

type LowLevelDefinition struct {
	Path       string
	PathPrefix string
	Handler    func(w http.ResponseWriter, r *http.Request)
}

type LowLevelHandlerDefinition struct {
	Path       string
	PathPrefix string
	Handler    http.Handler
}

type RouteDefinition struct {
	// Hosts scopes the route to these hosts — a service's <svc>.tau.<fqdn> plus
	// any custom domains bound via domains.hosts. Empty leaves it host-agnostic.
	Hosts       []string
	Path        string
	Vars        Variables
	Scope       []string
	Auth        RouteAuthHandler
	Handler     Handler
	RawResponse bool
}

type AssetsDefinition struct {
	Hosts                 []string // scope to these hosts (see RouteDefinition.Hosts)
	Path                  string
	Vars                  Variables
	Scope                 []string
	Auth                  RouteAuthHandler
	FileSystem            afero.Fs
	Directory             string
	SinglePageApplication bool

	BeforeServe func(w http.ResponseWriter)
}

type HeadlessAssetsDefinition struct {
	FileSystem            afero.Fs
	Directory             string
	SinglePageApplication bool

	BeforeServe func(w http.ResponseWriter)
}

type WebSocketHandler interface {
	In()
	Out()
}

type WebSocketConnection interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteJSON(v interface{}) error
	WriteMessage(messageType int, data []byte) error
	Close() error
	EnableWriteCompression(enable bool)
	SetCloseHandler(func(code int, text string) error)
}

type Variables struct {
	Required []string
	Optional []string
}

type Context interface {
	HandleWith(handler Handler) error
	HandleAuth(Handler) error
	HandleCleanup(Handler) error
	Request() *http.Request
	Writer() http.ResponseWriter
	ParseBody(obj interface{}) error

	RawResponse() bool
	SetRawResponse(val bool)

	Variables() map[string]interface{}
	SetVariable(key string, val interface{})

	Body() []byte
	SetBody([]byte)

	GetStringVariable(key string) (string, error)
	GetStringArrayVariable(key string) ([]string, error)
	GetStringMapVariable(key string) (map[string]interface{}, error)
	GetIntVariable(key string) (int, error)
}

type WebSocketDefinition struct {
	Hosts      []string // scope to these hosts (see RouteDefinition.Hosts)
	Path       string
	Vars       Variables
	Scope      []string
	Auth       RouteAuthHandler
	NewHandler func(ctx Context, conn WebSocketConnection) WebSocketHandler
}

type Service interface {
	Context() context.Context
	Start()
	Stop()
	Wait() error
	Error() error
	/********************************/
	GET(*RouteDefinition)
	PUT(*RouteDefinition)
	POST(*RouteDefinition)
	DELETE(*RouteDefinition)
	PATCH(*RouteDefinition)
	ALL(*RouteDefinition)

	Raw(*RawRouteDefinition) *mux.Route
	LowLevel(*LowLevelDefinition) *mux.Route
	LowLevelHandler(*LowLevelHandlerDefinition) *mux.Route

	WebSocket(*WebSocketDefinition)

	ServeAssets(*AssetsDefinition)

	AssetHandler(*HeadlessAssetsDefinition, Context) (interface{}, error)
	LowLevelAssetHandler(*HeadlessAssetsDefinition, http.ResponseWriter, *http.Request) error

	GetListenAddress() (*url.URL, error)
}
