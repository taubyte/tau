package accounts

// InferHost returns the bare hostname the accounts HTTP service binds on:
// `accounts.tau.<NetworkFqdn>` in production, `accounts.localhost` in dev.
func InferHost(devMode bool, rootDomain string) string {
	if devMode || rootDomain == "" {
		return "accounts.localhost"
	}
	return "accounts.tau." + rootDomain
}

// InferURL is the https:// form of InferHost. Embedded in magic-link emails
// and in the "no tau account linked" rejection from GitHubTokenHTTPAuth.
func InferURL(devMode bool, rootDomain string) string {
	return "https://" + InferHost(devMode, rootDomain)
}

// WebAuthnDefaults is the WebAuthn relying-party identity, derived from the
// runtime FQDN. RPID must match the host browsers see — that's why it's
// derived rather than configured.
type WebAuthnDefaults struct {
	RPID    string
	RPName  string
	Origins []string
}

func InferWebAuthn(devMode bool, rootDomain string) WebAuthnDefaults {
	return WebAuthnDefaults{
		RPID:    InferHost(devMode, rootDomain),
		RPName:  "Tau",
		Origins: []string{InferURL(devMode, rootDomain)},
	}
}
