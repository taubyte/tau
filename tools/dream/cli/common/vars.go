package common

import "github.com/taubyte/tau/dream"

var (
	DefaultDreamURL   = func() string { return "http://" + dream.DreamApiListen() }
	DefaultClientName = "client"
	ValidSubBinds     = []string{"http", "p2p", "dns", "https", "verbose", "copies"}
)
