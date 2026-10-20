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

// authapi.go is the public door to auth's sign-in and user calls.
//
// AUTH HAS NO INGRESS, and gains none: it answers other tau nodes only, so the
// sign-in a browser or the CLI makes is carried here and handed to it over
// p2p. What is carried is ConnectRPC unchanged — this file does not read a
// message, it only decides which calls pass:
//
//   - ONLY THE PROCEDURES NAMED BELOW, by their generated constants. Admission,
//     which nodes ask of auth, is not one of them.
//   - ONLY POST, with a body bounded at the point of reading.
//   - ONLY THE HEADERS a Connect call needs. Authorization is the caller's
//     TAU token here, because these calls are auth's own; no forge token is
//     forwarded.
//   - BEHIND THE SAME LIMITS as every other route a caller reaches: the global
//     bucket, then one per client address, since a sign-in has no credential
//     yet to key by.

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
	authv2connect.SessionServiceDisconnectProcedure,
	authv2connect.SessionServiceConnectionsProcedure,
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
