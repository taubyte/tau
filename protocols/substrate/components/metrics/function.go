package metrics

import (
	"bytes"
	"encoding/binary"
	"errors"
)

func (m *Function) Less(comp Metric) bool {
	switch n := comp.(type) {
	case *Function:
		return (m.Memory < 1 && n.Memory >= 1) || (m.Cached < n.Cached) || (m.ColdStart < n.ColdStart) || (m.AvgRunTime > n.AvgRunTime) || (m.Memory < n.Memory)

	default:
		return false
	}
}

func (m *Function) Encode() []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, EncodingVersion)
	binary.Write(&buf, binary.LittleEndian, m.Cached)
	binary.Write(&buf, binary.LittleEndian, m.ColdStart)
	binary.Write(&buf, binary.LittleEndian, m.Memory)
	binary.Write(&buf, binary.LittleEndian, m.AvgRunTime)
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

	return nil
}
