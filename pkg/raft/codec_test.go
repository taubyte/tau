package raft

// snapshotPayload is a snapshot as the tests build and read one.
type snapshotPayload struct {
	Data  map[string]CRDTEntry
	Clock uint64
}

func marshalPayload(p snapshotPayload) ([]byte, error) { return encodeSnapshot(p.Data, p.Clock) }

func unmarshalPayload(raw []byte, p *snapshotPayload) error {
	data, clock, err := decodeSnapshot(raw)
	p.Data, p.Clock = data, clock
	return err
}
