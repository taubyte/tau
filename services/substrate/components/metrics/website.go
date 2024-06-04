package metrics

import (
	"bytes"
	"encoding/binary"
	"errors"
)

func (m *Website) Less(comp Iface) bool {
	switch n := comp.(type) {
	case *Website:
		return m.Cached < n.Cached
	default:
		return false
	}
}

func (w *Website) Encode() []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, EncodingVersion)
	binary.Write(&buf, binary.LittleEndian, w.Cached)
	return buf.Bytes()
}

func (w *Website) Decode(b []byte) error {
	buf := bytes.NewBuffer(b)

	var encodingVersion uint8

	if err := binary.Read(buf, binary.LittleEndian, &encodingVersion); err != nil {
		return err
	}

	if encodingVersion != EncodingVersion {
		return errors.New("version mismatch")
	}

	if err := binary.Read(buf, binary.LittleEndian, &w.Cached); err != nil {
		return err
	}

	return nil
}
