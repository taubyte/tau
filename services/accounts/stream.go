package accounts

// setupStreamRoutes wires the accounts service's P2P stream verbs.
//
// Two integration verbs (verify, resolve) drive services/auth and the
// project compiler. The management verbs (account, member, user, plan)
// drive the Member CLI + future operator tooling. Login drives the
// magic-link / passkey flow.
func (srv *AccountsService) setupStreamRoutes() {
	srv.stream.Define(StreamVerbVerify, srv.apiVerifyHandler)
	srv.stream.Define(StreamVerbResolve, srv.apiResolveHandler)

	srv.stream.Define(StreamVerbAccount, srv.apiAccountHandler)
	srv.stream.Define(StreamVerbMember, srv.apiMemberHandler)
	srv.stream.Define(StreamVerbUser, srv.apiUserHandler)
	srv.stream.Define(StreamVerbPlan, srv.apiPlanHandler)
	srv.stream.Define(StreamVerbLogin, srv.apiLoginHandler)
}
