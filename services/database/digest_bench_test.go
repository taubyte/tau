package database

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"
)

// BenchmarkTheDigestUnderBothHashes measures what a proof of possession would
// cost. The gate compares digests to decide whether a node may delete its copy,
// and an echo of somebody else's answer satisfies it — so the fix is a
// nonce-salted digest, which xxhash cannot carry and a cryptographic hash can.
//
// THE QUESTION IS WHETHER THE SCAN OR THE HASH DOMINATES. digestOf already does
// a List plus a Get per key, so if the hash is noise against the scan the change
// costs nothing that matters.
func BenchmarkTheDigestUnderBothHashes(b *testing.B) {
	for _, rows := range []int{1_000, 100_000} {
		keys := make([]string, rows)
		values := make([][]byte, rows)
		for i := range keys {
			keys[i] = fmt.Sprintf("session:%08d", i)
			values[i] = make([]byte, 256)
			binary.BigEndian.PutUint64(values[i], uint64(i))
		}

		b.Run(fmt.Sprintf("xxhash/%d", rows), func(b *testing.B) {
			var field [8]byte
			for b.Loop() {
				sum := xxhash.New()
				for i := range keys {
					binary.BigEndian.PutUint64(field[:], uint64(len(keys[i])))
					sum.Write(field[:])
					sum.Write([]byte(keys[i]))
					binary.BigEndian.PutUint64(field[:], uint64(len(values[i])))
					sum.Write(field[:])
					sum.Write(values[i])
				}
				sum.Sum(nil)
			}
		})

		b.Run(fmt.Sprintf("sha256/%d", rows), func(b *testing.B) {
			var field [8]byte
			for b.Loop() {
				sum := sha256.New()
				for i := range keys {
					binary.BigEndian.PutUint64(field[:], uint64(len(keys[i])))
					sum.Write(field[:])
					sum.Write([]byte(keys[i]))
					binary.BigEndian.PutUint64(field[:], uint64(len(values[i])))
					sum.Write(field[:])
					sum.Write(values[i])
				}
				sum.Sum(nil)
			}
		})
	}
}

// BenchmarkDigestOfAgainstItsScan puts the hash next to the work it rides on.
// digestOf does a List and then a Get PER KEY, so the question the hash choice
// turns on is which of the two dominates — and this is against an in-memory map,
// which is the most favourable case a real replicated store will never match.
func BenchmarkDigestOfAgainstItsScan(b *testing.B) {
	engine := &accounting{}
	conn := &accounted{e: engine, rows: map[string][]byte{}}
	for i := range 100_000 {
		value := make([]byte, 256)
		conn.rows[fmt.Sprintf("session:%08d", i)] = value
	}

	ctx := b.Context()
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := digestOf(ctx, conn); err != nil {
			b.Fatal(err)
		}
	}
}
