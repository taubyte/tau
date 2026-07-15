//go:build web3
// +build web3

package ethereum

import (
	"context"
	"crypto/ecdsa"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) ethPubKeyFromSignedMessage(
	ctx context.Context,
	module common.Module,
	messagePtr, messageSize,
	signaturePtr, signatureSize,
	pubKeyPtr uint32,
) uint32 {
	message, err0 := f.ReadBytes(module, messagePtr, messageSize)
	if err0 != 0 {
		return uint32(err0)
	}

	signature, err0 := f.ReadBytes(module, signaturePtr, signatureSize)
	if err0 != 0 {
		return uint32(err0)
	}

	messageHash := crypto.Keccak256Hash(message)

	publicKey, err := crypto.Ecrecover(messageHash.Bytes(), signature)
	if err != nil {
		return uint32(errno.ErrorEthereumRecoverPubKeyFailed)
	}

	return uint32(f.WriteBytes(module, pubKeyPtr, publicKey))
}

func (f *Factory) ethHexToECDSA(
	ctx context.Context,
	module common.Module,
	hexStringPtr, hexStringLen,
	bufPtr uint32,
) uint32 {
	hexString, err0 := f.ReadString(module, hexStringPtr, hexStringLen)
	if err0 != 0 {
		return uint32(err0)
	}

	privKey, err := crypto.HexToECDSA(hexString)
	if err != nil {
		return uint32(errno.ErrorEthereumInvalidHexKey)
	}

	return uint32(f.WriteBytes(module, bufPtr, privKey.D.Bytes()))
}

func (f *Factory) ethPubFromPriv(
	ctx context.Context,
	module common.Module,
	privKeyPtr, PrivKeySize,
	bufPtr uint32,
) uint32 {
	pkBytes, err0 := f.ReadBytes(module, privKeyPtr, PrivKeySize)
	if err0 != 0 {
		return uint32(err0)
	}

	pk, err := crypto.ToECDSA(pkBytes)
	if err != nil {
		return uint32(errno.ErrorEthereumInvalidPrivateKey)
	}

	publicKey, ok := pk.Public().(*ecdsa.PublicKey)
	if !ok {
		return uint32(errno.ErrorEthereumInvalidPublicKey)
	}

	return uint32(f.WriteBytes(module, bufPtr, crypto.FromECDSAPub(publicKey)))
}
