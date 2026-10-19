package frontend

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

type fakeRegistrar struct {
	registered []string
	created    []string
	failCreate error
	failReg    error
}

func (f *fakeRegistrar) Register(_ context.Context, key string) error {
	if f.failReg != nil {
		return f.failReg
	}
	f.registered = append(f.registered, key)
	return nil
}

func (f *fakeRegistrar) Create(_ context.Context, name, config, code string) (string, error) {
	if f.failCreate != nil {
		return "", f.failCreate
	}
	f.created = append(f.created, name+"|"+config+"|"+code)
	return "proj-" + name, nil
}

func linkRequestFor(token, name, body string) *http.Request {
	r := authed(http.MethodPost, "/projects/"+name, token)
	r.Body = http.NoBody
	if body != "" {
		r = withBody(r, []byte(body))
	}
	return r
}

const linkBody = `{"config_repository_key":"github.com/acme/cfg","code_repository_key":"github.com/acme/pay"}`

func TestLinkingRegistersBothThenCreates(t *testing.T) {
	reg := &fakeRegistrar{}
	h := newHarness(t, Config{}, WithRegistrar(reg))

	w := h.do(t, linkRequestFor(testToken, "pay", linkBody))
	require.Equal(t, http.StatusCreated, w.Code)
	require.JSONEq(t, `{"id":"proj-pay","name":"pay"}`, bodyOf(t, w))
	require.Equal(t, []string{"github.com/acme/cfg", "github.com/acme/pay"}, reg.registered)
	require.Equal(t, []string{"pay|github.com/acme/cfg|github.com/acme/pay"}, reg.created)
}

// Write to one of the two is not enough: my config over your code would build
// your code under my project.
func TestLinkingNeedsWriteToBoth(t *testing.T) {
	reg := &fakeRegistrar{}
	h := newHarness(t, Config{}, WithRegistrar(reg))
	h.identity.write = func(_ string, key repokey.Key) (Access, error) {
		if strings.HasSuffix(key.String(), "/pay") {
			return AccessDenied, nil
		}
		return AccessGranted, nil
	}

	w := h.do(t, linkRequestFor(testToken, "pay", linkBody))
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, reg.registered, "nothing reached auth")
}

func TestLinkingRefusals(t *testing.T) {
	reg := &fakeRegistrar{}
	h := newHarness(t, Config{}, WithRegistrar(reg))

	require.Equal(t, http.StatusUnauthorized, h.do(t, linkRequestFor("", "pay", linkBody)).Code)
	require.Equal(t, http.StatusBadRequest, h.do(t, linkRequestFor(testToken, "pay", "{")).Code)
	require.Equal(t, http.StatusBadRequest, h.do(t, linkRequestFor(testToken, "pay",
		`{"config_repository_key":"not a key","code_repository_key":"github.com/acme/pay"}`)).Code)
	require.Empty(t, reg.registered)

	reg.failReg = connect.NewError(connect.CodePermissionDenied, errors.New("someone else's"))
	require.Equal(t, http.StatusNotFound, h.do(t, linkRequestFor(testToken, "pay", linkBody)).Code)

	reg.failReg = nil
	reg.failCreate = connect.NewError(connect.CodeUnavailable, errors.New("down"))
	require.Equal(t, http.StatusServiceUnavailable, h.do(t, linkRequestFor(testToken, "pay", linkBody)).Code)
}

// A frontend composed without a registrar does not say it lacks one.
func TestLinkingWithoutARegistrarIsADenial(t *testing.T) {
	h := newHarness(t, Config{})
	require.Equal(t, http.StatusNotFound, h.do(t, linkRequestFor(testToken, "pay", linkBody)).Code)

	_, err := New(Config{}, WithRegistrar(nil))
	require.Error(t, err)
}
