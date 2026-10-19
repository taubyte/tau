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
}

func (f *fakeAdmissions) Admit(context.Context, string, repokey.Key) (bool, string, error) {
	f.asked++
	return f.admitted, f.reason, f.err
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
