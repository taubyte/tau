package common

import "time"

const DatabaseName string = "seer"

var (
	NodeDatabaseFileName     string = "node-database.db"
	IPKey                           = "IP"
	DefaultBlockTime                = 60 * time.Second
	ValidServiceResponseTime        = 5 * time.Minute
)
