package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
)

func TestTranslateIsTheWholeCodeTable(t *testing.T) {
	for _, tc := range []struct {
		code connect.Code
		want int
	}{
		{connect.CodeNotFound, http.StatusNotFound},
		{connect.CodePermissionDenied, http.StatusNotFound},
		{connect.CodeInvalidArgument, http.StatusBadRequest},
		{connect.CodeFailedPrecondition, http.StatusConflict},
		{connect.CodeResourceExhausted, http.StatusServiceUnavailable},
		{connect.CodeUnavailable, http.StatusServiceUnavailable},
		{connect.CodeDeadlineExceeded, http.StatusGatewayTimeout},
		{connect.CodeUnauthenticated, http.StatusBadGateway},
		{connect.CodeInternal, http.StatusBadGateway},
		{connect.CodeUnimplemented, http.StatusBadGateway},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			status, _ := translate(connect.NewError(tc.code, errors.New("x")))
			require.Equal(t, tc.want, status)
		})
	}

	status, _ := translate(errors.New("not a connect error"))
	require.Equal(t, http.StatusBadGateway, status)
}

// services/build/clients wraps the last peer's error with %w, so the code
// underneath is still found — an ErrNoBackend is a 503 and not a 502.
func TestAWrappedConnectCodeIsStillFound(t *testing.T) {
	noBackend := errors.New("build: no peer answered")
	wrapped := fmt.Errorf("%w on %s: %w", noBackend, "/tau/build/v1",
		connect.NewError(connect.CodeUnavailable, errors.New("not the leader")))

	status, _ := translate(wrapped)

	require.Equal(t, http.StatusServiceUnavailable, status)
	require.ErrorIs(t, wrapped, noBackend)
}

// A route that already concluded "not found" is not overridden by whatever code
// the call underneath happened to carry.
func TestADecidedAnswerWinsOverATranslatedOne(t *testing.T) {
	w := httptest.NewRecorder()
	respond(w, fmt.Errorf("%w: %w", errDenied, connect.NewError(connect.CodeInternal, errors.New("boom"))))

	require.Equal(t, http.StatusNotFound, w.Code)
	require.JSONEq(t, `{"error":"not found"}`, w.Body.String())
}

func TestNoAnswerEchoesTheCallersInput(t *testing.T) {
	w := httptest.NewRecorder()
	respond(w, connect.NewError(connect.CodeInvalidArgument, errors.New("<script>alert(1)</script> is not a job id")))

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.NotContains(t, w.Body.String(), "script")
	require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

// A 202 is not a failure and must not carry an "error" body.
func TestAnIgnoredDeliveryIsAcceptedAndNotAnError(t *testing.T) {
	w := httptest.NewRecorder()
	respond(w, ignored("ref is not built"))

	require.Equal(t, http.StatusAccepted, w.Code)
	require.Empty(t, w.Body.String())
	require.Equal(t, "ref is not built", w.Header().Get("X-Build-Ignored"))
}

func TestAnHTTPErrorSaysWhatItIs(t *testing.T) {
	require.Equal(t, "404 not found", errDenied.Error())
}
