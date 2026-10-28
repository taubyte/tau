package mst

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
)

var sizes = []int{1_000, 10_000, 100_000}

func benchTree(b *testing.B, n int) (*counting, *Tree, map[string][]byte) {
	s := newStore()
	m := map[string][]byte{}
	for i := range n {
		m[string(key(i*2))] = value(i * 2)
	}
	return s, build(b, s, m), m
}

func BenchmarkPut(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			_, tr, _ := benchTree(b, n)
			v := []byte("bench")
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if _, err := tr.Put(ctx, key((i%n)*2+1), v); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}

func BenchmarkDelete(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			_, tr, _ := benchTree(b, n)
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if _, err := tr.Delete(ctx, key((i%n)*2)); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}

func BenchmarkGet(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			_, tr, _ := benchTree(b, n)
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				if _, _, err := tr.Get(ctx, key((i%n)*2)); err != nil {
					b.Fatal(err)
				}
				i++
			}
		})
	}
}

// BenchmarkDiff diffs a tree against itself with ten keys changed.
func BenchmarkDiff(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			_, a, _ := benchTree(b, n)
			c := a
			for j := range 10 {
				c = put(b, c, key(j*n/10*2+1), []byte("changed"))
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := Diff(ctx, a, c, func(Change) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBuild(b *testing.B) {
	ctx := context.Background()
	for _, n := range sizes {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s := newStore()
			m := map[string][]byte{}
			for i := range n {
				m[string(key(i))] = value(i)
			}
			var keys [][]byte
			for _, k := range slices.Sorted(maps.Keys(m)) {
				keys = append(keys, []byte(k))
			}
			seq := func(yield func([]byte, []byte) bool) {
				for _, k := range keys {
					if !yield(k, m[string(k)]) {
						return
					}
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Build(ctx, s, seq); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
