package frontend

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
)

// authapi.go is the public door to auth's sign-in and user calls; auth itself has
// no ingress. ConnectRPC passes through unread, limited to the named procedures,
// POST with a bounded body, the headers a Connect call needs (Authorization is
// the caller's tau token), behind the global and per-address limits.

// AuthAPI carries one Connect call to auth and returns its answer.
type AuthAPI interface {
	Call(ctx context.Context, procedure string, header http.Header, body []byte) (status int, respHeader http.Header, respBody []byte, err error)
}

// WithAuthAPI serves auth's sign-in and user calls through this ingress.
func WithAuthAPI(a AuthAPI) Option {
	return func(o *options) error {
		if a == nil {
			return errors.New("build: a nil auth API")
		}
		o.authAPI = a
		return nil
	}
}

// authProcedures are the calls a caller may make of auth.
var authProcedures = []string{
	authv2connect.SessionServiceProvidersProcedure,
	authv2connect.SessionServiceLoginProcedure,
	authv2connect.SessionServiceRefreshProcedure,
	authv2connect.SessionServiceLogoutProcedure,
	authv2connect.SessionServiceAuthorizeProcedure,
	authv2connect.SessionServiceExchangeProcedure,
	authv2connect.SessionServiceDeviceStartProcedure,
	authv2connect.SessionServiceDevicePollProcedure,
	authv2connect.SessionServicePastedProcedure,
	authv2connect.SessionServiceWhoamiProcedure,
	authv2connect.SessionServiceRenewProcedure,
	authv2connect.UserServiceListUsersProcedure,
	authv2connect.UserServiceCreateUserProcedure,
	authv2connect.UserServiceDeleteUserProcedure,
	authv2connect.UserServiceSetRoleProcedure,
	authv2connect.UserServiceSetDisabledProcedure,
	authv2connect.UserServiceSetPasswordProcedure,
	authv2connect.UserServiceCreateInviteProcedure,
	authv2connect.UserServiceListInvitesProcedure,
	authv2connect.UserServiceDeleteInviteProcedure,
	authv2connect.UserServiceCreateTokenProcedure,
	authv2connect.UserServiceListTokensProcedure,
	authv2connect.UserServiceRevokeTokenProcedure,
}

// The headers a Connect unary call carries, in each direction.
var (
	authRequestHeaders  = []string{"Content-Type", "Connect-Protocol-Version", "Connect-Timeout-Ms", "Authorization"}
	authResponseHeaders = []string{"Content-Type"}
)

// maxAuthBody bounds what a stranger's sign-in call may make this process read.
const maxAuthBody = 64 << 10

func (s *Service) handleAuth(procedure string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.globalJobs.allow() || !s.allowCaller("addr:"+clientAddr(r)) {
			respond(w, tooManyRequests(s.cfg.CallerRate.Per))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAuthBody))
		if err != nil {
			respond(w, problem(http.StatusRequestEntityTooLarge, "request body too large"))
			return
		}
		h := http.Header{}
		for _, k := range authRequestHeaders {
			if v := r.Header.Get(k); v != "" {
				h.Set(k, v)
			}
		}
		status, rh, rb, err := s.authAPI.Call(r.Context(), procedure, h, body)
		if err != nil {
			s.log("auth call %s: %v", procedure, err)
			respond(w, problem(http.StatusBadGateway, "auth is unreachable"))
			return
		}
		for _, k := range authResponseHeaders {
			if v := rh.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(status)
		w.Write(rb)
	}
}

// clientAddr is the peer's address without its port.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}
