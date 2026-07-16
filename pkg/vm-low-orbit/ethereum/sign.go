//go:build web3
// +build web3

package ethereum

import (
	"bytes"
	"context"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/taubyte/go-sdk/errno"
	eth "github.com/taubyte/go-sdk/ethereum/client/bytes"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) ethSignMessage(
	ctx context.Context,
	module common.Module,
	messagePtr, messageSize,
	privKeyPtr, privKeySize,
	signaturePtr uint32,
) uint32 {
	message, err0 := f.ReadBytes(module, messagePtr, messageSize)
	if err0 != 0 {
		return uint32(err0)
	}

	pkBytes, err0 := f.ReadBytes(module, privKeyPtr, privKeySize)
	if err0 != 0 {
		return uint32(err0)
	}

	pk, err := crypto.ToECDSA(pkBytes)
	if err != nil {
		return uint32(errno.ErrorEthereumInvalidPrivateKey)
	}

	hash := crypto.Keccak256Hash([]byte(message))

	sig, err := crypto.Sign(hash.Bytes(), pk)
	if err != nil {
		return uint32(errno.ErrorEthereumSignFailed)
	}

	return uint32(f.WriteBytes(module, signaturePtr, sig))
}

func (f *Factory) ethVerifySignature(
	ctx context.Context,
	module common.Module,
	messagePtr, messageSize,
	pubKeyPtr, pubKeySize,
	signaturePtr,
	verifiedPtr uint32,
) uint32 {
	message, err0 := f.ReadBytes(module, messagePtr, messageSize)
	if err0 != 0 {
		return uint32(err0)
	}

	signature, err0 := f.ReadBytes(module, signaturePtr, eth.EcdsaSignatureLength)
	if err0 != 0 {
		return uint32(err0)
	}

	pubKeyBytes, err0 := f.ReadBytes(module, pubKeyPtr, pubKeySize)
	if err0 != 0 {
		return uint32(err0)
	}

	sigPubKey, err := crypto.Ecrecover(crypto.Keccak256Hash([]byte(message)).Bytes(), signature)
	if err != nil {
		return uint32(errno.ErrorEthereumRecoverPubKeyFailed)
	}

	return uint32(f.WriteBool(module, verifiedPtr, bytes.Equal(sigPubKey, pubKeyBytes)))
}
