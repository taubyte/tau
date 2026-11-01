package gateway

import (
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/specs"
)

// CORS and rate limiting are read off the matched gateway resource: they are
// per-name policy, so an operator cannot turn a project's CORS off and a project
// cannot raise its own limit past what it wrote.

// cors answers a preflight and decorates a response. The matching origin is echoed,
// never wildcarded: `*` cannot carry credentials and is a different policy.
func cors(w http.ResponseWriter, r *http.Request, policy specs.GatewayCors, self string) {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}

	allowed := slices.Clip(policy.Origins)
	if own := policy.AllowSelf; own != nil && *own && self != "" {
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

	if methods := policy.Methods; len(methods) > 0 {
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

// limitKey is what the limit is counted per: "ip", or "header:<name>". An unknown
// key counts everything together, so a misspelling cannot silently disable the
// limit.
func limitKey(spec string, r *http.Request) string {
	if name, ok := strings.CutPrefix(spec, "header:"); ok {
		// Normalised once, so `header: X-Api-Key` is not a second bucket.
		name = strings.TrimSpace(name)
		return "h/" + strings.ToLower(name) + "/" + r.Header.Get(name)
	}
	return "ip/" + clientIP(r)
}

// clientIP is the peer address and never a forwarded header. X-Forwarded-For is
// client-controlled, and trusting it would let a caller dodge the limit with a new
// value per request.
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
