package gateway

import (
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/project"
)

// policy.go is what a gateway does at the name, before and after the route:
// CORS and rate limiting.
//
// BOTH ARE READ OFF THE MATCHED GATEWAY RESOURCE. They are per-name policy in
// the vocabulary, not per-node settings, so an operator cannot turn a project's
// CORS off and a project cannot raise its own rate limit past what it wrote.

// cors answers a preflight and decorates a response.
//
// THE ORIGIN IS ECHOED, NEVER WILDCARDED. `Access-Control-Allow-Origin: *`
// cannot carry credentials and is a different policy from the list an author
// wrote; echoing the one that matched says exactly what the config says.
func cors(w http.ResponseWriter, r *http.Request, gateway project.Resource, self string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	allowed := gateway.Strings("cors", "origins")
	if own, _ := gateway.Bool("cors", "allow_self"); own && self != "" {
		// The name this gateway itself answers on. It is the only way to say
		// it when the name is generated, because the cloud's half of it is not
		// knowable when the config is written.
		allowed = append(allowed, "https://"+self, "http://"+self)
	}

	if !originAllowed(allowed, origin) {
		return
	}

	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Origin", origin)

	if methods := gateway.Strings("cors", "methods"); len(methods) > 0 {
		w.Header().Set("Access-Control-Allow-Methods", strings.Join(methods, ", "))
	}
	if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
		w.Header().Set("Access-Control-Allow-Headers", requested)
	}
}

func originAllowed(allowed []string, origin string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}

// limitKey is what the limit is counted per: "ip", or "header:<name>".
//
// AN UNKNOWN KEY COUNTS EVERYTHING TOGETHER rather than counting nothing. A
// misspelled key that silently disabled the limit is the failure mode worth
// avoiding: it looks configured and is not.
func limitKey(spec string, r *http.Request) string {
	if name, ok := strings.CutPrefix(spec, "header:"); ok {
		// TRIMMED AND LOWERCASED ONCE. Trimming only for the lookup made
		// `header:X-Api-Key` and `header: X-Api-Key` two buckets for one
		// header, which is a caller doubling their budget with a space.
		name = strings.TrimSpace(name)
		return "h/" + strings.ToLower(name) + "/" + r.Header.Get(name)
	}
	return "ip/" + clientIP(r)
}

// clientIP is the peer address and never a forwarded header.
//
// X-Forwarded-For IS CLIENT-CONTROLLED unless something in front of the gateway
// is known to rewrite it, and this listener does not know that. Trusting it
// would let one caller spend every other caller's budget by picking a new value
// per request — which is the rate limit removed, by the requests it exists to
// stop.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// retryAfter is how long a limited caller should wait: one token's worth,
// rounded up to a whole second, and at least one.
func retryAfter(rpm int) string {
	if rpm <= 0 {
		return "1"
	}
	seconds := 60 / rpm
	if 60%rpm != 0 {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
