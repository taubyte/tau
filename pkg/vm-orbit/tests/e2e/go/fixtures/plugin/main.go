package main

import (
	"context"
	"strconv"

	"github.com/taubyte/tau/pkg/vm-orbit/satellite"
)

type tester struct{}

var addVal uint32 = 42

func (t *tester) W_add42(ctx context.Context, module satellite.Module, stringPtr uint32, lenPtr uint32) uint32 {
	data, err := module.MemoryRead(stringPtr, lenPtr)
	if err != nil {
		panic(err)
	}

	val, err := strconv.Atoi(string(data))
	if err != nil {
		panic(err)
	}

	return uint32(val) + addVal
}

func (t *tester) W_readWritePlus1(
	ctx context.Context,
	module satellite.Module,
	bytePtr,
	byteRcvPtr,
	byteSlicePtr,
	byteSliceSize,
	byteSliceRcvPtr,
	stringPtr,
	stringSize,
	stringRcvPtr,
	stringSlicePtr,
	stringSliceSize,
	stringSliceRcvPtr,
	u16Ptr,
	u16Rcv,
	u32Ptr,
	u32Rcv,
	u64Ptr,
	u64Rcv uint32,
) {
	byteVal, err := module.ReadByte(bytePtr)
	if err != nil {
		panic(err)
	}

	if _, err = module.WriteByte(byteRcvPtr, byteVal+1); err != nil {
		panic(err)
	}

	bytesSliceVal, err := module.ReadBytesSlice(byteSlicePtr, byteSliceSize)
	if err != nil {
		panic(err)
	}

	if _, err = module.WriteBytesSlice(byteSliceRcvPtr, bytesSliceVal); err != nil {
		panic(err)
	}

	stringVal, err := module.ReadString(stringPtr, stringSize)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteString(stringRcvPtr, stringVal+"one"); err != nil {
		panic(err)
	}

	stringSliceVal, err := module.ReadStringSlice(stringSlicePtr, stringSliceSize)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteStringSlice(stringSliceRcvPtr, stringSliceVal); err != nil {
		panic(err)
	}

	u16val, err := module.ReadUint16(u16Ptr)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteUint16(u16Rcv, u16val+1); err != nil {
		panic(err)
	}

	u32val, err := module.ReadUint32(u32Ptr)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteUint32(u32Rcv, u32val+1); err != nil {
		panic(err)
	}

	u64val, err := module.ReadUint64(u64Ptr)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteUint64(u64Rcv, u64val+1); err != nil {
		panic(err)
	}
}

func (t *tester) W_readWriteSize(
	ctx context.Context,
	module satellite.Module,
	bytesSlicePtr,
	bytesSliceSize,
	bytesSliceSizePtr,
	stringPtr,
	stringSize,
	stringSizePtr,
	stringSlicePtr,
	stringSliceSize,
	stringSliceSizePtr uint32,
) {
	bytesSliceVal, err := module.ReadBytesSlice(bytesSlicePtr, bytesSliceSize)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteBytesSliceSize(bytesSliceSizePtr, bytesSliceVal); err != nil {
		panic(err)
	}

	stringVal, err := module.ReadString(stringPtr, stringSize)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteStringSize(stringSizePtr, stringVal); err != nil {
		panic(err)
	}

	stringSliceVal, err := module.ReadStringSlice(stringSlicePtr, stringSliceSize)
	if err != nil {
		panic(err)
	}

	if _, err := module.WriteStringSliceSize(stringSliceSizePtr, stringSliceVal); err != nil {
		panic(err)
	}
}

func main() {
	satellite.Export("testing", &tester{})
}
