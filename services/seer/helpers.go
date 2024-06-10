package seer

import (
	"fmt"

	peercore "github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/p2p/streams/command"
)

func validateSignature(body command.Body) (string, bool, error) {
	// Grab Id's and Signature from body
	nodeId := body["id"]
	clientId := body["client"]
	signature := body["signature"]

	nodeIDStr, ok := nodeId.(string)
	if !ok {
		return "", false, fmt.Errorf("could not transform nodeId to string")
	}

	peerId, err := peercore.Decode(nodeIDStr)
	if err != nil {
		return "", false, fmt.Errorf("failed decoding `%s` with: %s", nodeIDStr, err)
	}

	clientIDStr, ok := clientId.(string)
	if !ok {
		return "", false, fmt.Errorf("could not transform clientId to string")
	}

	sigBytes, ok := signature.([]byte)
	if !ok {
		return "", false, fmt.Errorf("could not transform signature to []byte")
	}

	id, err := peercore.FromCid(peercore.ToCid(peerId))
	if err != nil {
		return "", false, fmt.Errorf("fromcid failed with: %s", err)
	}

	// Get public key
	pubKey, err := id.ExtractPublicKey()
	if err != nil {
		return "", false, fmt.Errorf("extract public key failed with: %s", err)
	}

	valid, err := pubKey.Verify([]byte(peerId.String()+clientIDStr), sigBytes)
	if err != nil {
		return "", false, fmt.Errorf("verify public key failed with: %s", err)
	}

	// Verify Signature and id's
	return peerId.String(), valid, nil
}
