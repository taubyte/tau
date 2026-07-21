//go:build !ee

package accounts

// setupStreamRoutesEE is a no-op in the community build — there are no ee
// verbs. The ee build registers them (see stream_ee.go).
func (srv *AccountsService) setupStreamRoutesEE() {}
