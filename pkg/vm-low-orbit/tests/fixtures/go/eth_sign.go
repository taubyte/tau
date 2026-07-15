//go:build eth_sign

package main

//lint:file-ignore U1000 compiled file

import (
	"bytes"
	"fmt"
	"strings"

	ethereum "github.com/taubyte/go-sdk/ethereum/client"
	"github.com/taubyte/go-sdk/event"
)

//export signTest
func signTest(e event.Event) (err0 uint32) {
	testPrivateKey := "d95da681814cba888f4d5258d38cb73cffe10baeadbdf04b7ace76de3a9b9ca7"
	testAddress := "0xB2c977Cf2cEb8f501eEAfA59Bc8f9919D5c61959"
	jsSignature := "0x537c6f928918d6b5d1b33f5e6845ee0e5487202d157fa4cf07d679fd861e77cb3a95f732f7475a5598ca5b467e6bbae91fabf43096c5c27853a0f970da25eb481b"
	message := "hello world"

	h, err := e.HTTP()
	if err != nil {
		panic(err)
	}

	errReturn := func(msg string) uint32 {
		h.Write([]byte(msg))
		h.Return(404)
		return 1
	}

	privKey, err := ethereum.HexToECDSABytes(testPrivateKey)
	if err != nil {
		return errReturn(fmt.Sprintf("converting hex key to priv key failed with: %s", err))
	}

	signature, err := ethereum.SignMessage([]byte(message), privKey)
	if err != nil {
		return errReturn(fmt.Sprintf("Signing message `%s` failed with: %s", message, err))
	}

	pubKey, err := ethereum.PublicKeyFromPrivate(privKey)
	if err != nil {
		return errReturn(fmt.Sprintf("getting public key from private key failed with: %s", err))
	}

	pubKey0, err := ethereum.PublicKeyFromSignedMessage([]byte(message), signature)
	if err != nil {
		return errReturn(fmt.Sprintf("getting public key from signed message failed with: %s", err))
	}

	if !bytes.Equal(pubKey, pubKey0) {
		return errReturn("pub keys not the same")
	}

	address := ethereum.AddressFromPubKey(pubKey)
	if address.String() != strings.ToLower(testAddress) {
		return errReturn(fmt.Sprintf("expected address `%s` got `%s`", testAddress, address.String()))
	}

	err = ethereum.VerifySignature([]byte(message), pubKey, signature)
	if err != nil {
		return errReturn(fmt.Sprintf("Verifying signature for message `%s` failed with: %s", message, err))
	}

	signature, err = ethereum.ParseSignature(jsSignature)
	if err != nil {
		return errReturn(fmt.Sprintf("parsing signature failed with: %s", err))
	}

	pubKey1, err := ethereum.PublicKeyFromSignedMessage(ethereum.ToEthJsMessage(message), signature)
	if err != nil {
		return errReturn(fmt.Sprintf("public key from metamask signed message failed with: %s", err))
	}

	address = ethereum.AddressFromPubKey(pubKey1)
	if address.String() != strings.ToLower(testAddress) {
		return errReturn(fmt.Sprintf("expected address `%s` got `%s`", testAddress, address.String()))
	}

	h.Return(205)

	return 0
}
