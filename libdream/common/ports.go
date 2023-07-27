package common

var (
	DefaultAuthPort      = 121
	DefaultHoarderPort   = 142
	DefaultMonkeyPort    = 163
	DefaultPatrickPort   = 184
	DefaultSeerPort      = 205
	DefaultTNSPort       = 226
	DefaultSubstratePort = 282
	DefaultDnsPort       = 304

	DefaultSeerHttpPort      = 403
	DefaultPatrickHttpPort   = 424
	DefaultAuthHttpPort      = 445
	DefaultTNSHttpPort       = 466
	DefaultSubstrateHttpPort = 529

	DreamlandApiListen = DefaultHost + ":1421"
)

var (
	DefaultHost             string = "127.0.0.1"
	DefaultP2PListenFormat  string = "/ip4/" + DefaultHost + "/tcp/%d"
	DefaultHTTPListenFormat string = "%s://" + DefaultHost + ":%d"
)
