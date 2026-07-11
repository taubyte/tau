//go:build ee

package hoarder

import (
	"bytes"
	"testing"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// TestCipher_SeamWiring proves the cipher seam wiring: kvPut stores ciphertext
// on the underlying kvdb and kvGet transparently decrypts it. newTestService
// seeds a fixed atRestKey, so no auth service is needed here (BootstrapKey is
// covered separately in the ee/services/hoarder/cipher integration test).
func TestCipher_SeamWiring(t *testing.T) {
	srv := newTestService(t)
	ctx := t.Context()
	hash := instanceHash(hoarderIface.MetaData{ProjectId: "kvp", Match: "/kv"})
	handle, err := srv.load(hash)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("plaintext secret")
	put := kvBody(hoarderSpecs.KVPut)
	put[hoarderSpecs.BodyKey] = "k1"
	put[hoarderSpecs.BodyValue] = plaintext
	if _, err := srv.kvPut(ctx, handle, hash, put); err != nil {
		t.Fatal(err)
	}

	// What's actually on disk must be ciphertext, not the plaintext.
	raw, err := handle.Get(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, plaintext) {
		t.Fatal("value stored in plaintext under -tags ee")
	}

	// kvGet must return the decrypted plaintext.
	get := kvBody(hoarderSpecs.KVGet)
	get[hoarderSpecs.BodyKey] = "k1"
	resp, err := srv.kvGet(ctx, handle, get)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp[hoarderSpecs.BodyValue].([]byte), plaintext) {
		t.Fatalf("kvGet did not decrypt: %q", resp[hoarderSpecs.BodyValue])
	}
}
