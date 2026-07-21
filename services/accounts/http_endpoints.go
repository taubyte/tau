//go:build !ee

package accounts

// setupHTTPRoutesEE is a no-op in the community build — the ee HTTP
// surface lives in http_endpoints_ee.go.
func (srv *AccountsService) setupHTTPRoutesEE(_ []string) {}
