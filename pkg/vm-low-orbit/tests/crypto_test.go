package tests

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"testing"

	plugins "github.com/taubyte/tau/pkg/vm-low-orbit"
)

func TestCryptoSha256(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Let's see if crypto is okay on wasm side")
	req := httptest.NewRequest("POST", "/sha256", bytes.NewReader(plaintext))
	w, ret := guestCall(t, ctx, "crypto", "crypto_sha256", req, testCtxOpts()...)

	if ret != 0 {
		t.Fatalf("crypto_sha256 returned %d (body: %s)", ret, w.Body.String())
	}

	// Compute expected SHA256 hash
	expected := sha256.Sum256(plaintext)
	if got := w.Body.Bytes(); !bytes.Equal(got, expected[:]) {
		t.Errorf("hash mismatch\n  got:      %x\n  expected: %x", got, expected)
	}
}

func TestCryptoBlowfishEnc(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Let's see if crypto is okay on wasm side")
	req := httptest.NewRequest("POST", "/blowfish/enc?key=taubyte", bytes.NewReader(plaintext))
	w, ret := guestCall(t, ctx, "crypto", "crypto_blowfish_enc", req, testCtxOpts()...)

	if ret != 0 {
		t.Fatalf("crypto_blowfish_enc returned %d (body: %s)", ret, w.Body.String())
	}

	ciphertext := w.Body.Bytes()
	if len(ciphertext) == 0 {
		t.Error("ciphertext is empty")
	}

	// Ciphertext should be at least 8 bytes (IV) + padded plaintext
	if len(ciphertext) <= 8 {
		t.Errorf("ciphertext too short: %d bytes", len(ciphertext))
	}
}

func TestCryptoBlowfishDec(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Let's see if crypto is okay on wasm side")

	// First encrypt the plaintext
	encReq := httptest.NewRequest("POST", "/blowfish/enc?key=taubyte", bytes.NewReader(plaintext))
	encW, encRet := guestCall(t, ctx, "crypto", "crypto_blowfish_enc", encReq, testCtxOpts()...)

	if encRet != 0 {
		t.Fatalf("crypto_blowfish_enc returned %d", encRet)
	}

	ciphertext := encW.Body.Bytes()

	// Now decrypt the ciphertext
	decReq := httptest.NewRequest("POST", "/blowfish/dec?key=taubyte", bytes.NewReader(ciphertext))
	decW, decRet := guestCall(t, ctx, "crypto", "crypto_blowfish_dec", decReq, testCtxOpts()...)

	if decRet != 0 {
		t.Fatalf("crypto_blowfish_dec returned %d (body: %s)", decRet, decW.Body.String())
	}

	decrypted := decW.Body.Bytes()

	// The decrypted text should start with the original plaintext
	// (it may have padding, but the original plaintext should be there)
	if !bytes.HasPrefix(decrypted, plaintext) {
		t.Errorf("decrypted plaintext mismatch\n  expected prefix: %s\n  got: %s", string(plaintext), string(decrypted))
	}
}

func TestCryptoBlowfishRoundTrip(t *testing.T) {
	ctx := context.Background()
	if err := plugins.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	testCases := []string{
		"hello",
		"Let's see if crypto is okay on wasm side",
		"a",
		"12345678", // Exactly blowfish.BlockSize
	}

	for _, plaintext := range testCases {
		t.Run(fmt.Sprintf("plaintext=%s", plaintext), func(t *testing.T) {
			pt := []byte(plaintext)

			// Encrypt
			encReq := httptest.NewRequest("POST", "/blowfish/enc?key=secretkey", bytes.NewReader(pt))
			encW, encRet := guestCall(t, ctx, "crypto", "crypto_blowfish_enc", encReq, testCtxOpts()...)

			if encRet != 0 {
				t.Fatalf("encrypt returned %d", encRet)
			}

			ciphertext := encW.Body.Bytes()
			if len(ciphertext) == 0 {
				t.Fatal("ciphertext is empty")
			}

			// Decrypt
			decReq := httptest.NewRequest("POST", "/blowfish/dec?key=secretkey", bytes.NewReader(ciphertext))
			decW, decRet := guestCall(t, ctx, "crypto", "crypto_blowfish_dec", decReq, testCtxOpts()...)

			if decRet != 0 {
				t.Fatalf("decrypt returned %d", decRet)
			}

			decrypted := decW.Body.Bytes()

			// Check that the plaintext is recovered
			if !bytes.HasPrefix(decrypted, pt) {
				t.Errorf("round-trip failed: plaintext not in decrypted output\n  plaintext: %s\n  decrypted: %s", plaintext, string(decrypted))
			}
		})
	}
}
