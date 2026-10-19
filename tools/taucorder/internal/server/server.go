// Package server is the local listener: the v2 services' own ConnectRPC,
// forwarded unchanged to the swarm.
//
// Everything a request crosses on the way in is a refusal point, in order:
// the Host header (DNS rebinding), the Origin header (other web pages), the
// method policy (what the bridge's swarm authority is lent out for). Only a
// request that passes all three reaches the reverse proxy. Refusals are
// written with connect's own ErrorWriter, so a generated client sees a normal
// connect, grpc or grpc-web error with a code.
package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strings"

	"connectrpc.com/connect"
	connectcors "connectrpc.com/cors"

	"github.com/taubyte/tau/tools/taucorder/internal/policy"
	"github.com/taubyte/tau/tools/taucorder/internal/route"
)

// Options is what the operator opened.
type Options struct {
	Allow policy.Allow
	// Origins are the browser origins allowed to call, exactly as a browser
	// sends them ("http://localhost:5173"). "*" is not accepted.
	Origins []string
	// Hosts are the Host header values accepted besides loopback names.
	Hosts []string
}

// Handler is the listener's http.Handler.
type Handler struct {
	opts  Options
	proxy *httputil.ReverseProxy
	errw  *connect.ErrorWriter
}

// New forwards through rt.
func New(rt http.RoundTripper, opts Options) (*Handler, error) {
	if slices.Contains(opts.Origins, "*") {
		return nil, errors.New("server: a wildcard origin would lend the swarm to every web page")
	}
	h := &Handler{opts: opts, errw: connect.NewErrorWriter()}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "tau"
			// Nothing about the browser travels into the swarm.
			for _, k := range []string{"Origin", "Cookie", "Authorization", "Referer"} {
				pr.Out.Header.Del(k)
			}
		},
		Transport:     rt,
		FlushInterval: -1, // server streams flush every message
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			code := connect.CodeUnavailable
			if errors.Is(err, r.Context().Err()) && r.Context().Err() != nil {
				code = connect.CodeCanceled
			}
			h.errw.Write(w, r, connect.NewError(code, err))
		},
	}
	return h, nil
}

func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, code connect.Code, format string, args ...any) {
	if !h.errw.IsSupported(r) {
		http.Error(w, fmt.Sprintf(format, args...), connectHTTP(code))
		return
	}
	h.errw.Write(w, r, connect.NewError(code, fmt.Errorf(format, args...)))
}

func connectHTTP(code connect.Code) int {
	switch code {
	case connect.CodePermissionDenied:
		return http.StatusForbidden
	case connect.CodeUnimplemented:
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

func (h *Handler) hostAllowed(host string) bool {
	name := host
	if hn, _, err := net.SplitHostPort(host); err == nil {
		name = hn
	}
	name = strings.Trim(name, "[]")
	if name == "localhost" {
		return true
	}
	if ip := net.ParseIP(name); ip != nil && ip.IsLoopback() {
		return true
	}
	return slices.Contains(h.opts.Hosts, host) || slices.Contains(h.opts.Hosts, name)
}

func (h *Handler) cors(w http.ResponseWriter, origin string) {
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", strings.Join(connectcors.AllowedMethods(), ", "))
	w.Header().Set("Access-Control-Allow-Headers", strings.Join(connectcors.AllowedHeaders(), ", "))
	w.Header().Set("Access-Control-Expose-Headers", strings.Join(connectcors.ExposedHeaders(), ", "))
	w.Header().Set("Access-Control-Max-Age", "7200")
}

// ServeHTTP refuses, or forwards.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.hostAllowed(r.Host) {
		h.refuse(w, r, connect.CodePermissionDenied, "host %q is not this bridge", r.Host)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if !slices.Contains(h.opts.Origins, origin) {
			h.refuse(w, r, connect.CodePermissionDenied, "origin %q is not allowed", origin)
			return
		}
		h.cors(w, origin)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	svc, method, err := policy.Parse(r.URL.Path)
	if err != nil {
		h.refuse(w, r, connect.CodeUnimplemented, "%v", err)
		return
	}
	class := policy.Of(svc, method)
	if class == policy.Unknown {
		h.refuse(w, r, connect.CodeUnimplemented, "%s/%s is not a known tau v2 method", svc, method)
		return
	}
	if !h.opts.Allow.Permits(class) {
		h.refuse(w, r, connect.CodePermissionDenied, "%s/%s is a %s call; this bridge was not started to allow it", svc, method, class)
		return
	}
	h.proxy.ServeHTTP(w, r)
}

// Protocols is what the listener speaks: HTTP/1.1 for browsers and grpc-web,
// cleartext HTTP/2 for grpc and streaming clients.
func Protocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return p
}

var _ http.RoundTripper = (*route.Transport)(nil)
