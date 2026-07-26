//go:build !ee

package hoarder

import (
	"strings"
	"testing"

	golog "github.com/ipfs/go-log/v2"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestCipher_Identity(t *testing.T) {
	srv := newTestService(t)
	enc, err := srv.cipherEncrypt([]byte("x"))
	if err != nil || string(enc) != "x" {
		t.Fatalf("community cipher encrypt must be identity: %q, %v", enc, err)
	}
	dec, err := srv.cipherDecrypt([]byte("y"))
	if err != nil || string(dec) != "y" {
		t.Fatalf("community cipher decrypt must be identity: %q, %v", dec, err)
	}
	if err := srv.admitWrite("proj", 1024); err != nil {
		t.Fatalf("community admission must accept: %v", err)
	}
}

// TestCipher_PlaintextWarning proves the at-rest-plaintext warning fires once,
// at cipherInit, and never again from cipherEncrypt/cipherDecrypt — a warning
// in the storage hot path would be a serious regression.
func TestCipher_PlaintextWarning(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	prev := golog.GetConfig()
	golog.SetPrimaryCore(core)
	if err := golog.SetLogLevel("tau.hoarder.service", "warn"); err != nil {
		t.Fatalf("setting log level failed: %v", err)
	}
	t.Cleanup(func() { golog.SetupLogging(prev) })

	srv := newTestService(t)
	if err := srv.cipherInit(t.Context(), nil); err != nil {
		t.Fatalf("cipherInit failed: %v", err)
	}

	// Storage hot path: must not add any further warnings.
	for range 5 {
		if _, err := srv.cipherEncrypt([]byte("v")); err != nil {
			t.Fatalf("cipherEncrypt failed: %v", err)
		}
		if _, err := srv.cipherDecrypt([]byte("v")); err != nil {
			t.Fatalf("cipherDecrypt failed: %v", err)
		}
	}

	var matches int
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "unencrypted") {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly 1 plaintext-at-rest warning, got %d", matches)
	}
}
