package common

import "github.com/ipfs/go-log/v2"

var (
	BroadcastInterval   = 5
	DefaultDatabaseName = "DatabaseService"
	Logger              = log.Logger("substrate.service.database")
)
