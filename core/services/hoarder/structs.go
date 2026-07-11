package hoarder

// ResourceKind identifies what a placement/registry entry is for. Global is a
// project-wide database hosted by hoarders without per-resource TNS validation.
type ResourceKind int

const (
	Database ResourceKind = iota
	Storage
	Global
)

// Auction carries a placement request: the resource kind plus the identity/
// config metadata used to validate and hash the instance. Deterministic HRW
// placement replaced the timed-bidding protocol this type is named after, so it
// now only carries that request payload.
type Auction struct {
	MetaType ResourceKind
	Meta     MetaData
}

type MetaData struct {
	ConfigId      string
	ProjectId     string
	ApplicationId string
	Match         string
	Branch        string
}
