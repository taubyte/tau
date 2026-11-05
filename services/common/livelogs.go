package common

// LiveLogsTopic carries a running job's log as the monkey writes it, so
// Patrick can stream it to clients before the job ends and its log is stored.
const LiveLogsTopic = "/patrick/logs/v1"

// LiveLogChunk is a piece of a job's log starting at Offset bytes into it.
// The last chunk of a job has Done set and may carry no data.
type LiveLogChunk struct {
	Job    string `cbor:"1,keyasint"`
	Offset int64  `cbor:"2,keyasint"`
	Data   []byte `cbor:"3,keyasint"`
	Done   bool   `cbor:"4,keyasint"`
}
