//go:build !ee

package hoarder

import "testing"

func TestCipher_OSSIdentity(t *testing.T) {
	srv := newTestService(t)
	enc, err := srv.cipherEncrypt([]byte("x"))
	if err != nil || string(enc) != "x" {
		t.Fatalf("OSS cipher encrypt must be identity: %q, %v", enc, err)
	}
	dec, err := srv.cipherDecrypt([]byte("y"))
	if err != nil || string(dec) != "y" {
		t.Fatalf("OSS cipher decrypt must be identity: %q, %v", dec, err)
	}
	if err := srv.admitWrite("proj", 1024); err != nil {
		t.Fatalf("OSS admission must accept: %v", err)
	}
}
