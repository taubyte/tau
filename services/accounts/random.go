package accounts

import "crypto/rand"

// cryptoRandRead is a tiny indirection for crypto/rand.Reader.Read. Lets the
// webauthn session-id generator (and any future randomness consumer in the
// package) be tested with deterministic byte streams when needed.
func cryptoRandRead(b []byte) (int, error) {
	return rand.Read(b)
}
