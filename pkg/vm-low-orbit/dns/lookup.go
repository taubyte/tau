package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) dnsLookupTxTSize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	records, err0 := resolver.LookupTXT(f.ctx, name)
	if err0 != nil {
		return uint32(errno.ErrorFailedTxTLookup)
	}

	resolver.cacheResponse(TxTResponse, name, records)

	return uint32(f.WriteStringSliceSize(module, sizePtr, records))
}

func (f *Factory) dnsLookupTxT(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	recordPtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	resp, err := resolver.getCachedResponse(TxTResponse, name)
	if err != 0 {
		return uint32(errno.ErrorFailedTxTLookup)
	}

	return uint32(f.WriteStringSlice(module, recordPtr, resp))
}

func (f *Factory) dnsLookupAddressSize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	addr, err0 := resolver.LookupAddr(f.ctx, name)
	if err0 != nil {
		return uint32(errno.ErrorFailedAddressLookup)
	}

	resolver.cacheResponse(AddressResponse, name, addr)

	return uint32(f.WriteStringSliceSize(module, sizePtr, addr))
}

func (f *Factory) dnsLookupAddress(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	recordPtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	resp, err := resolver.getCachedResponse(AddressResponse, name)
	if err != 0 {
		return uint32(errno.ErrorFailedTxTLookup)
	}

	return uint32(f.WriteStringSlice(module, recordPtr, resp))
}

func (f *Factory) dnsLookupCNAMESize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	cname, err0 := resolver.LookupCNAME(f.ctx, name)
	if err0 != nil {
		return uint32(errno.ErrorFailedCNAMELookup)
	}

	cnameResp := make([]string, 0)
	cnameResp = append(cnameResp, cname)

	resolver.cacheResponse(CnameResponse, name, cnameResp)

	return uint32(f.WriteStringSize(module, sizePtr, cname))
}

func (f *Factory) dnsLookupCNAME(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	recordPtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	resp, err := resolver.getCachedResponse(CnameResponse, name)
	if err != 0 {
		return uint32(errno.ErrorFailedTxTLookup)
	}

	//CNAME will always be len 1
	return uint32(f.WriteString(module, recordPtr, resp[0]))
}

func (f *Factory) dnsLookupMXSize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	mxRecords, err0 := resolver.LookupMX(f.ctx, name)
	if err0 != nil {
		return uint32(errno.ErrorFailedMXLookup)
	}

	mxRecordList := make([]string, 0)
	for _, mx := range mxRecords {
		mxString := strings.Join([]string{mx.Host, fmt.Sprint(mx.Pref) + "/"}, "/")
		mxRecordList = append(mxRecordList, mxString)
	}

	resolver.cacheResponse(MxResponse, name, mxRecordList)

	return uint32(f.WriteStringSliceSize(module, sizePtr, mxRecordList))
}

func (f *Factory) dnsLookupMX(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	mxPtr uint32,
) uint32 {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return uint32(errno.ErrorResolverNotFound)
	}

	resp, err := resolver.getCachedResponse(MxResponse, name)
	if err != 0 {
		return uint32(errno.ErrorFailedTxTLookup)
	}

	return uint32(f.WriteStringSlice(module, mxPtr, resp))
}
