package common

import "github.com/taubyte/tau/dream"

var (
	DefaultDreamlandURL = "http://" + dream.DreamlandApiListen
	DefaultUniverseName = "blackhole"
	DefaultClientName   = "client"
	DoDaemon            = false
	ValidSubBinds       = []string{"http", "p2p", "dns", "https", "verbose", "copies"}
)
