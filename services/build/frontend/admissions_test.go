package frontend

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

type fakeAdmissions struct {
	admitted bool
	reason   string
	err      error
	asked    int
	last     Credentials
}

func (f *fakeAdmissions) Admit(_ context.Context, c Credentials, _ repokey.Key) (bool, string, error) {
	f.asked++
	f.last = c
	return f.admitted, f.reason, f.err
}

// The tau token reaches admission beside the forge token, never instead of
// it.
func TestTheTauTokenReachesAdmission(t *testing.T) {
	a := &fakeAdmissions{admitted: true}
	h := newHarness(t, Config{}, WithAdmissions(a))
	r := authed(http.MethodGet, "/job/"+testJobID, testToken)
	r.Header.Set(TauTokenHeader, " tau-access ")
	w := h.do(t, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, Credentials{Forge: testToken, Tau: "tau-access"}, a.last)

	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, Credentials{Forge: testToken}, a.last)
}

func TestAnAdmittedCallerIsServed(t *testing.T) {
	a := &fakeAdmissions{admitted: true}
	h := newHarness(t, Config{}, WithAdmissions(a))
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, a.asked)
}

func TestACallerTheCloudDoesNotAdmitIsToldWhy(t *testing.T) {
	h := newHarness(t, Config{}, WithAdmissions(&fakeAdmissions{reason: "sign up at https://accounts.example.com"}))
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "sign up at https://accounts.example.com")

	h = newHarness(t, Config{}, WithAdmissions(&fakeAdmissions{}))
	w = h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "not admitted")
}

func TestAnAdmissionThatCannotBeDecidedIsNotADenial(t *testing.T) {
	h := newHarness(t, Config{}, WithAdmissions(&fakeAdmissions{err: errors.New("auth unreachable")}))
	w := h.do(t, authed(http.MethodGet, "/job/"+testJobID, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestANilAdmissionsIsRefused(t *testing.T) {
	require.Error(t, WithAdmissions(nil)(&options{}))
}
