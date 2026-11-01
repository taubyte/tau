package database

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"
)

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
