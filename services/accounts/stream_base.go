package accounts

// setupStreamRoutes wires the accounts service's P2P stream verbs.
//
// The verify verb drives services/auth. The management verbs (account,
// member, user) drive the Member CLI + operator tooling. Login drives the
// magic-link / passkey flow. The ee build registers additional verbs via
// setupStreamRoutesEE (a no-op here).
func (srv *AccountsService) setupStreamRoutes() {
	srv.stream.Define(StreamVerbVerify, srv.apiVerifyHandler)
	srv.stream.Define(StreamVerbLookupAccountsByEmail, srv.apiLookupAccountsByEmailHandler)

	srv.stream.Define(StreamVerbAccount, srv.apiAccountHandler)
	srv.stream.Define(StreamVerbMember, srv.apiMemberHandler)
	srv.stream.Define(StreamVerbUser, srv.apiUserHandler)
	srv.stream.Define(StreamVerbLogin, srv.apiLoginHandler)

	srv.setupStreamRoutesEE()
}
