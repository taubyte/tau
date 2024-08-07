package patrick

import "sync"

type Job struct {
	Id        string    `cbor:"1,keyasint"`
	Timestamp int64     `cbor:"2,keyasint"`
	Status    JobStatus `cbor:"3,keyasint"`
	LogLock   sync.Mutex
	Logs      map[string]string `cbor:"11,keyasint"` // Cid of logs
	Meta      Meta              `cbor:"15,keyasint"`
	CidLock   sync.Mutex
	AssetCid  map[string]string `cbor:"20,keyasint"` // Build Cid
	Attempt   int               `cbor:"30,keyasint"`
	Delay     *DelayConfig      `cbor:"99,keyasint"` // Inject a delay to run a job
}

// TODO: optimize cbor storage
type Meta struct {
	Ref        string     `json:"ref" cbor:"4,keyasint"`
	Before     string     `json:"before" cbor:"8,keyasint"`
	After      string     `json:"after" cbor:"16,keyasint"`
	HeadCommit HeadCommit `json:"head_commit" cbor:"32,keyasint"`
	Repository Repository `cbor:"64,keyasint"`
}

type HeadCommit struct {
	ID string `json:"id" cbor:"33,keyasint"`
}

type Repository struct {
	ID       int    `json:"id" cbor:"65,keyasint"`
	Provider string `json:"provider" cbor:"66,keyasint"`
	SSHURL   string `json:"ssh_url" cbor:"67,keyasint"`
	Branch   string `json:"default_branch" cbor:"68,keyasint"`
}

type DelayConfig struct {
	Time int `cbor:"1,keyasint"` // Inject delay in second
}
