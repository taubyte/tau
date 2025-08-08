package common

import "github.com/taubyte/tau/dream"

var (
	DefaultDreamURL = "http://" + dream.DreamApiListen
	DefaultUniverseName = "blackhole"
	DefaultClientName   = "client"
	DoDaemon            = false
	ValidSubBinds       = []string{"http", "p2p", "dns", "https", "verbose", "copies"}
)
