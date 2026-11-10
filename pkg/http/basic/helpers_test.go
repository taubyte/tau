package basic

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	service "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/http/request"
)

// The request log runs after every call on every service; the caller's token is
// one of the request's variables, and the login flow sends it in the query.
func TestRequestLogHasNoToken(t *testing.T) {
	const secret = "SECRET123TOKEN"

	if err := logging.SetLogLevel("http.basic", "debug"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { logging.SetLogLevel("http.basic", "error") })

	pipe := logging.NewPipeReader()
	defer pipe.Close()

	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()

	httpReq := httptest.NewRequest(http.MethodGet, "/probe?session=x."+secret, nil)
	httpReq.Header.Set("Authorization", "github "+secret)
	req := &request.Request{ResponseWriter: httptest.NewRecorder(), HttpRequest: httpReq}

	ok := func(service.Context) (interface{}, error) { return nil, nil }
	(&Service{}).handleRequest(req, &service.Variables{}, []string{"test"}, ok, ok, nil)

	// The request is done; drain what was logged for it.
	sawRequest := false
	deadline := time.After(2 * time.Second)
	for !sawRequest {
		select {
		case line, open := <-lines:
			if !open {
				t.Fatal("log pipe closed early")
			}
			if strings.Contains(line, secret) {
				t.Fatalf("request log leaked the token: %s", line)
			}
			if strings.Contains(line, "/probe") {
				sawRequest = true
			}
		case <-deadline:
			t.Fatal("no request log line seen")
		}
	}
}
