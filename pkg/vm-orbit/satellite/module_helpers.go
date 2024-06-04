package satellite

import (
	"encoding/binary"

	"github.com/taubyte/go-sdk/utils/codec"
)

func (h *moduleLink) ReadByte(ptr uint32) (byte, error) {
	data, err := h.MemoryRead(ptr, 1)
	if err != nil {
		return 0, err
	}

	return data[0], nil
}

func (h *moduleLink) WriteByte(ptr uint32, val byte) (uint32, error) {
	data := [1]byte{val}
	return h.MemoryWrite(ptr, data[:])
}

func (h *moduleLink) ReadUint16(ptr uint32) (uint16, error) {
	data, err := h.MemoryRead(ptr, 2)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint16(data), nil
}

func (h *moduleLink) WriteUint16(ptr uint32, val uint16) (uint32, error) {
	var data [2]byte
	binary.LittleEndian.PutUint16(data[:], val)
	return h.MemoryWrite(ptr, data[:])
}

func (h *moduleLink) ReadUint32(ptr uint32) (uint32, error) {
	data, err := h.MemoryRead(ptr, 4)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint32(data), nil
}

func (h *moduleLink) WriteUint32(ptr uint32, val uint32) (uint32, error) {
	var data [4]byte
	binary.LittleEndian.PutUint32(data[:], val)
	return h.MemoryWrite(ptr, data[:])
}

func (h *moduleLink) ReadUint64(ptr uint32) (uint64, error) {
	data, err := h.MemoryRead(ptr, 8)
	if err != nil {
		return 0, err
	}

	return binary.LittleEndian.Uint64(data), nil
}

func (h *moduleLink) WriteUint64(ptr uint32, val uint64) (uint32, error) {
	var data [8]byte
	binary.LittleEndian.PutUint64(data[:], val)
	return h.MemoryWrite(ptr, data[:])
}

func (h *moduleLink) ReadString(ptr uint32, size uint32) (string, error) {
	data, err := h.MemoryRead(ptr, size)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func (h *moduleLink) WriteString(ptr uint32, val string) (uint32, error) {
	return h.MemoryWrite(ptr, []byte(val))

}

func (h *moduleLink) WriteStringSize(sizePtr uint32, val string) (uint32, error) {
	return h.WriteUint32(sizePtr, uint32(len(val)))
}

func (h *moduleLink) ReadStringSlice(ptr uint32, size uint32) ([]string, error) {
	data, err := h.MemoryRead(ptr, size)
	if err != nil {
		return nil, err
	}

	var slice []string
	err = codec.Convert(data).To(&slice)

	return slice, err
}

func (h *moduleLink) WriteStringSlice(ptr uint32, val []string) (uint32, error) {
	var data []byte
	if err := codec.Convert(val).To(&data); err != nil {
		return 0, err
	}

	return h.MemoryWrite(ptr, data)
}

func (h *moduleLink) WriteStringSliceSize(sizePtr uint32, val []string) (uint32, error) {
	var data []byte
	if err := codec.Convert(val).To(&data); err != nil {
		return 0, err
	}

	return h.WriteUint32(sizePtr, uint32(len(data)))
}

func (h *moduleLink) ReadBytesSlice(ptr uint32, size uint32) ([][]byte, error) {
	data, err := h.MemoryRead(ptr, size)
	if err != nil {
		return nil, err
	}

	var slice [][]byte
	err = codec.Convert(data).To(&slice)

	return slice, err
}

func (h *moduleLink) WriteBytesSlice(ptr uint32, val [][]byte) (uint32, error) {
	var data []byte
	if err := codec.Convert(val).To(&data); err != nil {
		return 0, err
	}

	return h.MemoryWrite(ptr, data)
}

func (h *moduleLink) WriteBytesSliceSize(sizePtr uint32, val [][]byte) (uint32, error) {
	var data []byte
	if err := codec.Convert(val).To(&data); err != nil {
		return 0, err
	}

	return h.WriteUint32(sizePtr, uint32(len(data)))
}
