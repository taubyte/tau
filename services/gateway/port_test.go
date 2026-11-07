package gateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

func TestARouteThatNamesAPortIsDeliveredOverIt(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout/cart", "an order")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "http", w.Header().Get("X-Port"), "the route's port, by name")
	require.Equal(t, "service/api", w.Header().Get("X-Served"))
	require.Equal(t, "/checkout/cart", w.Body.String())

	seen := h.Workers.seen()
	require.Len(t, seen, 1)
	require.Equal(t, "http", seen[0].port)
	require.Equal(t, "an order", string(seen[0].req.Body), "the body travels over the connection")
}

func TestARouteThatNamesNoPortIsInvoked(t *testing.T) {
	h := newHarness(t, Config{})

	w := h.do(t, http.MethodPost, "http://ask.example.com/", "a question")
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Header().Get("X-Port"))
	require.Empty(t, h.Workers.seen()[0].port)
}

func TestAPortThatCannotBeReachedIsAFailedRoute(t *testing.T) {
	h := newHarness(t, Config{})
	h.Workers.fail(errors.New("api declares no port called http"))

	w := h.do(t, http.MethodPost, "http://shop.example.com/checkout", "")
	require.GreaterOrEqual(t, w.Code, 500)
}

// hangUp is a workload whose port accepts and closes.
type hangUp struct{ recordingWorkers }

func (*hangUp) Conn(context.Context, specs.Address, string) (net.Conn, error) {
	client, workload := net.Pipe()
	workload.Close()
	return client, nil
}

func TestAWorkloadThatHangsUpIsAnError(t *testing.T) {
	s := &Service{workers: &hangUp{}}
	_, err := s.deliver(t.Context(), specs.Address{Kind: "service", Name: "api"}, "metrics", &invoke.Request{})
	require.ErrorContains(t, err, `service/api on port "metrics"`)

	_, err = s.deliver(t.Context(), specs.Address{Kind: "service", Name: "api"}, "metrics", &invoke.Request{Method: "BAD METHOD"})
	require.ErrorContains(t, err, "building the request")
}
