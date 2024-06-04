package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_dnsLookupTxTSize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	records, err0 := resolver.LookupTXT(f.ctx, name)
	if err0 != nil {
		return errno.ErrorFailedTxTLookup
	}

	resolver.cacheResponse(TxTResponse, name, records)

	return f.WriteStringSliceSize(module, sizePtr, records)
}

func (f *Factory) W_dnsLookupTxT(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	recordPtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	resp, err := resolver.getCachedResponse(TxTResponse, name)
	if err != 0 {
		return errno.ErrorFailedTxTLookup
	}

	return f.WriteStringSlice(module, recordPtr, resp)
}

func (f *Factory) W_dnsLookupAddressSize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	addr, err0 := resolver.LookupAddr(f.ctx, name)
	if err0 != nil {
		return errno.ErrorFailedAddressLookup
	}

	resolver.cacheResponse(AddressResponse, name, addr)

	return f.WriteStringSliceSize(module, sizePtr, addr)
}

func (f *Factory) W_dnsLookupAddress(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	recordPtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	resp, err := resolver.getCachedResponse(AddressResponse, name)
	if err != 0 {
		return errno.ErrorFailedTxTLookup
	}

	return f.WriteStringSlice(module, recordPtr, resp)
}

func (f *Factory) W_dnsLookupCNAMESize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	cname, err0 := resolver.LookupCNAME(f.ctx, name)
	if err0 != nil {
		return errno.ErrorFailedCNAMELookup
	}

	cnameResp := make([]string, 0)
	cnameResp = append(cnameResp, cname)

	resolver.cacheResponse(CnameResponse, name, cnameResp)

	return f.WriteStringSize(module, sizePtr, cname)
}

func (f *Factory) W_dnsLookupCNAME(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	recordPtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	resp, err := resolver.getCachedResponse(CnameResponse, name)
	if err != 0 {
		return errno.ErrorFailedTxTLookup
	}

	//CNAME will always be len 1
	return f.WriteString(module, recordPtr, resp[0])
}

func (f *Factory) W_dnsLookupMXSize(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	sizePtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	mxRecords, err0 := resolver.LookupMX(f.ctx, name)
	if err0 != nil {
		return errno.ErrorFailedMXLookup
	}

	mxRecordList := make([]string, 0)
	for _, mx := range mxRecords {
		mxString := strings.Join([]string{mx.Host, fmt.Sprint(mx.Pref) + "/"}, "/")
		mxRecordList = append(mxRecordList, mxString)
	}

	resolver.cacheResponse(MxResponse, name, mxRecordList)

	return f.WriteStringSliceSize(module, sizePtr, mxRecordList)
}

func (f *Factory) W_dnsLookupMX(
	ctx context.Context,
	module common.Module,
	resolverId,
	namePtr, nameLen,
	mxPtr uint32,
) errno.Error {
	name, err := f.ReadString(module, namePtr, nameLen)
	if err != 0 {
		return errno.ErrorAddressOutOfMemory
	}

	resolver, err := f.getResolver(resolverId)
	if err != 0 {
		return errno.ErrorResolverNotFound
	}

	resp, err := resolver.getCachedResponse(MxResponse, name)
	if err != 0 {
		return errno.ErrorFailedTxTLookup
	}

	return f.WriteStringSlice(module, mxPtr, resp)
}
