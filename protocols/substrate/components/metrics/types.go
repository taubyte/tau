package metrics

import (
	"bytes"
	"encoding/binary"
	"errors"
)

type Website struct {
	Cached float32
}

type Function struct {
	Cached     float32
	ColdStart  int64
	Memory     float64
	AvgRunTime int64
}

type Iface interface {
	Encode() []byte
	Decode(b []byte) error
	Less(Iface) bool
}

// Encoding/Decoding
//  - Append new metrics
//  - Don't do: Type or order change will require new EncodingVersion version

var EncodingVersion uint8 = 1

func (m Website) Less(comp Iface) bool {
	switch n := comp.(type) {
	case Website:
		return m.Cached < n.Cached
	default:
		return false
	}
}

func (m Website) Encode() []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, EncodingVersion)
	return buf.Bytes()
}

func (m Website) Decode(b []byte) error {
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

	return nil
}

func (m Function) Less(comp Iface) bool {
	switch n := comp.(type) {
	case Function:
		return (m.Memory < 1 && n.Memory >= 1) || (m.Cached < n.Cached) || (m.ColdStart < n.ColdStart) || (m.AvgRunTime > n.AvgRunTime) || (m.Memory < n.Memory)

	default:
		return false
	}
}

func (m Function) Encode() []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, EncodingVersion)
	binary.Write(&buf, binary.LittleEndian, m.Cached)
	binary.Write(&buf, binary.LittleEndian, m.ColdStart)
	binary.Write(&buf, binary.LittleEndian, m.Memory)
	binary.Write(&buf, binary.LittleEndian, m.AvgRunTime)
	return buf.Bytes()
}

func (m Function) Decode(b []byte) error {
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
