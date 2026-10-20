// Package password hashes the passwords tau keeps: the built-in admin's and
// those of local users who may sign in with one.
//
// THE HASH IS ARGON2ID OVER HMAC-SHA256(PEPPER, PASSWORD), with the cost
// parameters stored beside each hash so they can be raised later. The pepper
// lives in the secrets service and the hash in auth's store, so a copied store
// alone yields nothing a dictionary can be run against. Each hash names the
// pepper it was made with, so a cloud whose nodes each hold their own pepper
// verifies any of them.
//
// The admin named in node configuration is the one hash without a pepper: it
// is written by an operator before the cloud has a secrets service to hold
// one, and lives in configuration, not in the store a pepper protects. It is
// spelled in the PHC string format Argon2's reference implementation prints.
package password

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are Argon2id's cost parameters.
type Params struct {
	Time    uint32 `cbor:"t"`
	Memory  uint32 `cbor:"m"` // KiB
	Threads uint8  `cbor:"p"`
	KeyLen  uint32 `cbor:"k"`
}

// Default is RFC 9106's second recommended option (64 MiB, three passes),
// with two lanes.
var Default = Params{Time: 3, Memory: 64 * 1024, Threads: 2, KeyLen: 32}

// Hash is a stored password.
type Hash struct {
	// Pepper is the id of the pepper the hash was made with; empty is none.
	Pepper string `cbor:"pepper"`
	Params Params `cbor:"params"`
	Salt   []byte `cbor:"salt"`
	Sum    []byte `cbor:"sum"`
}

// ErrMismatch is a password that is not the one hashed.
var ErrMismatch = errors.New("password: mismatch")

// MinLength is the shortest password accepted.
const MinLength = 8

// MaxLength bounds what is hashed. Argon2 accepts any length, but a request
// should not be able to make a node hash a megabyte.
const MaxLength = 1024

// Check refuses a password too short or too long to keep.
func Check(pw string) error {
	switch {
	case len(pw) < MinLength:
		return fmt.Errorf("password: at least %d characters", MinLength)
	case len(pw) > MaxLength:
		return fmt.Errorf("password: at most %d bytes", MaxLength)
	}
	return nil
}

// New hashes pw with the pepper whose id is pepperID.
func New(pw string, pepperID string, pepper []byte, p Params) (Hash, error) {
	if err := Check(pw); err != nil {
		return Hash{}, err
	}
	if err := p.valid(); err != nil {
		return Hash{}, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return Hash{}, err
	}
	return Hash{Pepper: pepperID, Params: p, Salt: salt, Sum: sum(pw, pepper, salt, p)}, nil
}

// Verify compares in constant time. pepper must be the one h names.
func (h Hash) Verify(pw string, pepper []byte) error {
	if len(pw) > MaxLength || h.Params.valid() != nil || len(h.Sum) == 0 {
		return ErrMismatch
	}
	if subtle.ConstantTimeCompare(sum(pw, pepper, h.Salt, h.Params), h.Sum) != 1 {
		return ErrMismatch
	}
	return nil
}

// Stale says h was made with weaker parameters than p.
func (h Hash) Stale(p Params) bool {
	return h.Params.Time < p.Time || h.Params.Memory < p.Memory || h.Params.KeyLen < p.KeyLen
}

func sum(pw string, pepper, salt []byte, p Params) []byte {
	input := []byte(pw)
	if pepper != nil {
		mac := hmac.New(sha256.New, pepper)
		mac.Write(input)
		input = mac.Sum(nil)
	}
	return argon2.IDKey(input, salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}

// The bounds keep a stored or configured hash from naming a cost that takes a
// node down to verify.
const (
	maxTime   = 64
	maxMemory = 1 << 20 // 1 GiB
)

func (p Params) valid() error {
	switch {
	case p.Time < 1 || p.Time > maxTime:
		return fmt.Errorf("password: time %d outside 1..%d", p.Time, maxTime)
	case p.Memory < 8*uint32(p.Threads) || p.Memory > maxMemory:
		return fmt.Errorf("password: memory %d KiB outside %d..%d", p.Memory, 8*uint32(p.Threads), maxMemory)
	case p.Threads < 1:
		return errors.New("password: at least one thread")
	case p.KeyLen < 16 || p.KeyLen > 64:
		return fmt.Errorf("password: key length %d outside 16..64", p.KeyLen)
	}
	return nil
}

var b64 = base64.RawStdEncoding

// Encode is h in PHC form: $argon2id$v=19$m=…,t=…,p=…$salt$sum. A peppered
// hash has no PHC spelling; Encode refuses one.
func (h Hash) Encode() (string, error) {
	if h.Pepper != "" {
		return "", errors.New("password: a peppered hash has no PHC form")
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.Params.Memory, h.Params.Time, h.Params.Threads, b64.EncodeToString(h.Salt), b64.EncodeToString(h.Sum)), nil
}

// Parse reads a PHC-form argon2id hash.
func Parse(s string) (Hash, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Hash{}, errors.New("password: not a $argon2id$ PHC string")
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return Hash{}, fmt.Errorf("password: argon2 version %q, want v=%d", parts[2], argon2.Version)
	}
	var p Params
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return Hash{}, fmt.Errorf("password: parameter %q", kv)
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return Hash{}, fmt.Errorf("password: parameter %q: %w", kv, err)
		}
		switch k {
		case "m":
			p.Memory = uint32(n)
		case "t":
			p.Time = uint32(n)
		case "p":
			if n > 255 {
				return Hash{}, fmt.Errorf("password: parallelism %d", n)
			}
			p.Threads = uint8(n)
		default:
			return Hash{}, fmt.Errorf("password: unknown parameter %q", k)
		}
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return Hash{}, fmt.Errorf("password: salt: %w", err)
	}
	sum, err := b64.DecodeString(parts[5])
	if err != nil {
		return Hash{}, fmt.Errorf("password: hash: %w", err)
	}
	p.KeyLen = uint32(len(sum))
	if err := p.valid(); err != nil {
		return Hash{}, err
	}
	if len(salt) < 8 {
		return Hash{}, errors.New("password: salt shorter than 8 bytes")
	}
	return Hash{Params: p, Salt: salt, Sum: sum}, nil
}
