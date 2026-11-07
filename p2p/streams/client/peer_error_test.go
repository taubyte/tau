package client

import (
	"bytes"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	ce "github.com/taubyte/tau/p2p/streams/command/error"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/pkg/platformerr"
)

func roundTrip(t *testing.T, sent error) error {
	var buf bytes.Buffer
	if err := ce.Encode(&buf, sent); err != nil {
		t.Fatal(err)
	}
	resp, err := cr.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return peerError(peer.ID("p"), "cmd", resp)
}

func TestRewordedNotFoundIsStillNotFound(t *testing.T) {
	err := roundTrip(t, platformerr.New(platformerr.NotFound, errors.New("nothing is served here")))
	if platformerr.CodeOf(err) != platformerr.NotFound {
		t.Fatalf("code lost: %v", err)
	}
	if want := `peer 2w returned error for command "cmd": nothing is served here`; err.Error() != want {
		t.Fatalf("text changed: %q", err)
	}
}

func TestUncodedPeerErrorHasNoCode(t *testing.T) {
	err := peerError(peer.ID("p"), "cmd", cr.Response{"error": "x"})
	if err == nil || platformerr.CodeOf(err) != "" || err.Error() != `peer 2w returned error for command "cmd": x` {
		t.Fatalf("got %v", err)
	}
	if peerError(peer.ID("p"), "cmd", cr.Response{"ok": true}) != nil {
		t.Fatal("no error key is no error")
	}
}
