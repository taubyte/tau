package friendly

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v71/github"
	"github.com/taubyte/tau/dream"
	"github.com/taubyte/tau/pkg/platformerr"
)

func TestExplain(t *testing.T) {
	body := func(c string) error {
		return fmt.Errorf(`GET -- %s failed with status: 404 Not Found: {"code":404,"error":%q,"message":"x"}`, "`/x`", c)
	}
	reset := time.Date(2026, 11, 7, 14, 5, 0, 0, time.Local)
	for _, tc := range []struct {
		name string
		err  error
		want string // in msg+hint
	}{
		{"not_found", body("not_found"), "Nothing is deployed"},
		{"node_busy", body("node_busy"), "node is busy"},
		{"module_unavailable", body("module_unavailable"), "could not be loaded"},
		{"function_error", body("function_error"), "failed while running"},
		{"function_timeout", body("function_timeout"), "took too long"},
		{"rejected", body("rejected"), "rejected the request"},
		{"internal", body("internal"), "internal error"},
		{"typed code", fmt.Errorf("wrap: %w", platformerr.New(platformerr.NodeBusy, errors.New("x"))), "node is busy"},
		{"rate limit with reset", &github.RateLimitError{Rate: github.Rate{Reset: github.Timestamp{Time: reset}}, Response: &http.Response{Request: &http.Request{}}}, "resets at 14:05"},
		{"rate limit text", errors.New("403 API rate limit exceeded for user"), "tau doctor"},
		{"bad token", &github.ErrorResponse{Response: &http.Response{StatusCode: 401, Request: &http.Request{}}}, "tau login --new"},
		{"universe", &dream.UniverseNotFoundError{Name: "blackhol", Existing: []string{"blackhole"}}, "(blackhole)"},
		{"universe none", &dream.UniverseNotFoundError{Name: "x"}, "dream new universe x"},
		{"cli universe", errors.New("universe `foo` was not found"), `no universe "foo"`},
		{"project", errors.New("project not found"), "tau import project"},
		{"network", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "Could not reach"},
		{"network text", errors.New("Get http://127.0.0.1:1421/status: dial tcp 127.0.0.1:1421: connect: connection refused"), "Could not reach"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg, hint, ok := Explain(tc.err)
			if !ok || !strings.Contains(msg+" "+hint, tc.want) {
				t.Fatalf("Explain(%v) = %q, %q, %v; want %q", tc.err, msg, hint, ok, tc.want)
			}
		})
	}

	if _, _, ok := Explain(errors.New("something else")); ok {
		t.Fatal("unknown errors pass through")
	}
}

func TestErrorDebug(t *testing.T) {
	orig := errors.New("project not found: Qm123")
	defer func(d bool) { Debug = d }(Debug)

	Debug = false
	if s := Error(orig).Error(); strings.Contains(s, "Qm123") {
		t.Fatalf("original text shown without --debug: %s", s)
	}
	Debug = true
	if err := Error(orig); !errors.Is(err, orig) || !strings.Contains(err.Error(), "Qm123") {
		t.Fatalf("original lost with --debug: %v", err)
	}
}
