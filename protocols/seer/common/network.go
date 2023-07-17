package common

import dreamlandCommon "bitbucket.org/taubyte/dreamland/common"

var (
	DefaultP2PListenPort int = 4299
	DevHttpListenPort    int = 9000 + dreamlandCommon.DefaultSeerHttpPort
)

var (
	DefaultDnsPort    int = 53
	DefaultDevDnsPort int = 4253

	OraclePubSubPath = "/seer/oracle/v1"
)
