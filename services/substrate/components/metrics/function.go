package metrics

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	// unknownFree stands in for a node too old to report its load: middling,
	// so it is neither preferred over an idle node nor avoided next to a full one.
	unknownFree = 0.5
	// freeLevels groups free capacity coarsely, so a few requests of difference
	// do not outweigh a warm instance and send work to a cold node.
	freeLevels = 8
)

// Less says m is a worse node than comp, comparing one key at a time so sorting
// by it is a strict ordering: a node that cannot fit the function, then free
// capacity, then warmth, then speed, then free memory.
func (m *Function) Less(comp Iface) bool {
	n, ok := comp.(*Function)
	if !ok {
		return false
	}
	if fits, nFits := m.Memory >= 1, n.Memory >= 1; fits != nFits {
		return nFits
	}
	if free, nFree := int(m.Free*freeLevels), int(n.Free*freeLevels); free != nFree {
		return free < nFree
	}
	if m.Cached != n.Cached {
		return m.Cached < n.Cached
	}
	if m.ColdStart != n.ColdStart {
		return m.ColdStart > n.ColdStart
	}
	if m.AvgRunTime != n.AvgRunTime {
		return m.AvgRunTime > n.AvgRunTime
	}
	return m.Memory < n.Memory
}

func (m *Function) Encode() []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, EncodingVersion)
	binary.Write(&buf, binary.LittleEndian, m.Cached)
	binary.Write(&buf, binary.LittleEndian, m.ColdStart)
	binary.Write(&buf, binary.LittleEndian, m.Memory)
	binary.Write(&buf, binary.LittleEndian, m.AvgRunTime)
	binary.Write(&buf, binary.LittleEndian, m.Free)
	return buf.Bytes()
}

func (m *Function) Decode(b []byte) error {
	buf := bytes.NewBuffer(b)

	var encodingVersion uint8

	if err := binary.Read(buf, binary.LittleEndian, &encodingVersion); err != nil {
		return err
	}

	if encodingVersion != EncodingVersion {
		return errors.New("version mismatch")
	}

	if err := binary.Read(buf, binary.LittleEndian, &m.Cached); err != nil {
		return err
	}

	if err := binary.Read(buf, binary.LittleEndian, &m.ColdStart); err != nil {
		return err
	}

	if err := binary.Read(buf, binary.LittleEndian, &m.Memory); err != nil {
		return err
	}

	if err := binary.Read(buf, binary.LittleEndian, &m.AvgRunTime); err != nil {
		return err
	}

	m.Free = unknownFree
	if err := binary.Read(buf, binary.LittleEndian, &m.Free); err != nil && err != io.EOF {
		return err
	}

	return nil
}
