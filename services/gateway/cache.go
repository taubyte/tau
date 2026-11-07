package gateway

import (
	"container/list"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
)

// A route that says `cache:` has its GET and HEAD answers kept here, so a name served
// in many regions answers where the caller is without asking a handler that may run
// in one. What is kept is bounded by bytes, least recently used out first.
//
// THE KEY CARRIES THE RELEASE, the object a deployment runs now, so a deploy is never
// answered from the one before and nothing has to be invalidated: an old release's
// answers are never asked for again and age out.

// DefaultCacheBudget is how many bytes of answers a gateway keeps.
const DefaultCacheBudget = 64 << 20

// cacheHeader says whether an answer came from what this gateway keeps.
const cacheHeader = "X-Taubyte-Cache"

type answerKey struct {
	release register.ObjectID
	host    string
	method  string
	path    string
	query   string
	vary    string
}

type kept struct {
	key     answerKey
	status  int
	headers map[string][]string
	body    []byte
	stored  time.Time
	expires time.Time
}

func (k *kept) size() int64 {
	n := int64(len(k.body) + len(k.key.host) + len(k.key.path) + len(k.key.query) + len(k.key.vary))
	for name, values := range k.headers {
		n += int64(len(name))
		for _, v := range values {
			n += int64(len(v))
		}
	}
	return n
}

type answers struct {
	mu     sync.Mutex
	budget int64
	held   int64
	order  *list.List
	by     map[answerKey]*list.Element
}

func newAnswers(budget int64) *answers {
	return &answers{budget: budget, order: list.New(), by: map[answerKey]*list.Element{}}
}

// get is the answer kept under key, if it is still fresh at now.
func (a *answers) get(key answerKey, now time.Time) (*kept, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.by[key]
	if !ok {
		return nil, false
	}
	k := e.Value.(*kept)
	if !now.Before(k.expires) {
		a.drop(e)
		return nil, false
	}
	a.order.MoveToFront(e)
	return k, true
}

// put keeps an answer, making room for it. One larger than an eighth of the budget is
// not kept: it would push out everything else for one name.
func (a *answers) put(k *kept) {
	size := k.size()
	if size > a.budget/8 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.by[k.key]; ok {
		a.drop(e)
	}
	for a.held+size > a.budget {
		a.drop(a.order.Back())
	}
	a.by[k.key] = a.order.PushFront(k)
	a.held += size
}

func (a *answers) drop(e *list.Element) {
	k := a.order.Remove(e).(*kept)
	delete(a.by, k.key)
	a.held -= k.size()
}

// cacheKey is the key a request's answer is kept under, and whether the route keeps
// answers to it at all.
func cacheKey(m match, release register.ObjectID, host string, r *http.Request, req *invoke.Request) (answerKey, bool) {
	route := m.gateway.Route
	if route.CacheTtl == nil || *route.CacheTtl <= 0 {
		return answerKey{}, false
	}
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return answerKey{}, false
	}
	// A router decides per request, from more than the key says.
	if router, _ := m.router(); router != "" {
		return answerKey{}, false
	}
	var vary strings.Builder
	for _, name := range route.CacheVary {
		vary.WriteString(name)
		vary.WriteByte('=')
		vary.WriteString(strings.Join(http.Header(req.Headers).Values(name), ","))
		vary.WriteByte('\n')
	}
	return answerKey{release: release, host: host, method: req.Method, path: req.Path, query: r.URL.RawQuery, vary: vary.String()}, true
}

// keepable is how long an answer may be kept: the handler's own Cache-Control when it
// says, the route's ttl when it does not, and never for what is private to one caller.
func keepable(res *invoke.Response, ttl time.Duration) time.Duration {
	switch res.Status {
	case 0, http.StatusOK, http.StatusMovedPermanently, http.StatusNotFound:
	default:
		return 0
	}
	h := http.Header(res.Headers)
	if len(h.Values("Set-Cookie")) > 0 {
		return 0
	}
	maxAge, shared := time.Duration(-1), time.Duration(-1)
	for _, line := range h.Values("Cache-Control") {
		for _, directive := range strings.Split(line, ",") {
			name, value, _ := strings.Cut(strings.TrimSpace(directive), "=")
			switch strings.ToLower(name) {
			case "no-store", "no-cache", "private":
				return 0
			case "max-age":
				maxAge = seconds(value)
			case "s-maxage":
				shared = seconds(value)
			}
		}
	}
	switch {
	case shared >= 0:
		return shared
	case maxAge >= 0:
		return maxAge
	}
	return ttl
}

func seconds(value string) time.Duration {
	n, err := strconv.Atoi(strings.Trim(value, `"`))
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// served writes a kept answer, saying how old it is.
func (k *kept) served(w http.ResponseWriter, now time.Time) {
	w.Header().Set("Age", strconv.Itoa(int(now.Sub(k.stored)/time.Second)))
	w.Header().Set(cacheHeader, "hit")
	writeTo(w, &invoke.Response{Status: k.status, Headers: k.headers, Body: k.body})
}

// keep stores res under key when it may be kept, and marks it a miss either way.
func (s *Service) keep(w http.ResponseWriter, key answerKey, ttl time.Duration, res *invoke.Response) {
	w.Header().Set(cacheHeader, "miss")
	ttl = keepable(res, ttl)
	if ttl <= 0 {
		return
	}
	now := s.now()
	s.answers.put(&kept{key: key, status: res.Status, headers: res.Headers, body: res.Body, stored: now, expires: now.Add(ttl)})
}

// routeTtl is the route's ttl; cacheKey has said it is set.
func routeTtl(g specs.Gateway) time.Duration { return *g.Route.CacheTtl }
