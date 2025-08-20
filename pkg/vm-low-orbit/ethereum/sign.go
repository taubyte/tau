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

func (f *Factory) W_ethSignMessage(
	ctx context.Context,
	module common.Module,
	messagePtr, messageSize,
	privKeyPtr, privKeySize,
	signaturePtr uint32,
) errno.Error {
	message, err0 := f.ReadBytes(module, messagePtr, messageSize)
	if err0 != 0 {
		return err0
	}

	pkBytes, err0 := f.ReadBytes(module, privKeyPtr, privKeySize)
	if err0 != 0 {
		return err0
	}

	pk, err := crypto.ToECDSA(pkBytes)
	if err != nil {
		return errno.ErrorEthereumInvalidPrivateKey
	}

	hash := crypto.Keccak256Hash([]byte(message))

	sig, err := crypto.Sign(hash.Bytes(), pk)
	if err != nil {
		return errno.ErrorEthereumSignFailed
	}

	return f.WriteBytes(module, signaturePtr, sig)
}

func (f *Factory) W_ethVerifySignature(
	ctx context.Context,
	module common.Module,
	messagePtr, messageSize,
	pubKeyPtr, pubKeySize,
	signaturePtr,
	verifiedPtr uint32,
) errno.Error {
	message, err0 := f.ReadBytes(module, messagePtr, messageSize)
	if err0 != 0 {
		return err0
	}

	signature, err0 := f.ReadBytes(module, signaturePtr, eth.EcdsaSignatureLength)
	if err0 != 0 {
		return err0
	}

	pubKeyBytes, err0 := f.ReadBytes(module, pubKeyPtr, pubKeySize)
	if err0 != 0 {
		return err0
	}

	sigPubKey, err := crypto.Ecrecover(crypto.Keccak256Hash([]byte(message)).Bytes(), signature)
	if err != nil {
		return errno.ErrorEthereumRecoverPubKeyFailed
	}

	return f.WriteBool(module, verifiedPtr, bytes.Equal(sigPubKey, pubKeyBytes))
}
