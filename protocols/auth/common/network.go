package common

import dreamlandCommon "bitbucket.org/taubyte/dreamland/common"

var (
	DefaultP2PListenPort int = 4221
	DevHttpListenPort    int = 9000 + dreamlandCommon.DefaultAuthHttpPort
)
