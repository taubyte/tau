package common

import dreamlandCommon "github.com/taubyte/dreamland/core/common"

// TODO: These ports are tied to generic config/dreamland and will need looked at moving generic config
var (
	AuthDefaultP2PListenPort = 4221
	AuthDevHttpListenPort    = 9000 + dreamlandCommon.DefaultAuthHttpPort

	HoarderDefaultP2PListenPort = 4260

	NodeDefaultP2PListenPort = 4242
	NodeDevHttpListenPort    = 9000 + dreamlandCommon.DefaultNodeHttpPort

	PatrickDefaultP2PListenPort = 4222
	PatrickDevHttpListenPort    = 9000 + dreamlandCommon.DefaultPatrickHttpPort

	SeerDefaultP2PListenPort = 4299
	SeerDevHttpListenPort    = 9000 + dreamlandCommon.DefaultSeerHttpPort

	DefaultDnsPort    = 53
	DefaultDevDnsPort = 4253

	TnsDefaultP2PListenPort = 4253

	MonkeyDefaultP2PListenPort = 4270
)
