package patrick

import (
	"encoding/json"
	"sync"

	"github.com/fxamacker/cbor/v2"
)

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
	SSHURL   string `json:"ssh_url" cbor:"67,keyasint"` // deprecated: use URI; kept for backward compat
	URI      string `json:"uri" cbor:"69,keyasint"`
	Branch   string `json:"default_branch" cbor:"68,keyasint"`
}

// UnmarshalJSON decodes JSON and runs normalize so URI is set from SSHURL when empty.
func (r *Repository) UnmarshalJSON(data []byte) error {
	type repo Repository
	if err := json.Unmarshal(data, (*repo)(r)); err != nil {
		return err
	}
	r.normalize()
	return nil
}

// Unmarshal decodes CBOR data into the job and normalizes the repository (e.g. promotes SSHURL to URI).
func (j *Job) Unmarshal(data []byte) error {
	if err := cbor.Unmarshal(data, j); err != nil {
		return err
	}
	j.Meta.Repository.normalize()
	return nil
}

// normalize promotes SSHURL to URI when URI is empty (backward compat for old webhooks and stored jobs).
func (r *Repository) normalize() {
	if r.URI == "" && r.SSHURL != "" {
		r.URI = r.SSHURL
	}
}

type DelayConfig struct {
	Time int `cbor:"1,keyasint"` // Inject delay in second
}
