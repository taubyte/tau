package seer

import (
	"fmt"
	"testing"

	"github.com/spf13/afero"
)

// benchSeer builds an in-memory seer seeded with `docs` documents, each a
// small nested map — the shape schema/tcc actually reads and writes.
func benchSeer(b *testing.B, docs int) *Seer {
	b.Helper()
	fs := afero.NewMemMapFs()
	s, err := New(VirtualFS(fs, "/"))
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < docs; i++ {
		name := fmt.Sprintf("doc%d", i)
		err := s.Get(name).Document().Get("meta").Get("id").Set(name).Commit()
		if err != nil {
			b.Fatal(err)
		}
		if err := s.Get(name).Document().Get("meta").Get("name").Set("resource").Commit(); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.Sync(); err != nil {
		b.Fatal(err)
	}
	return s
}

// Read a nested scalar from an already-cached document — the dominant op in
// schema decode (thousands per config compile).
func BenchmarkGetValue(b *testing.B) {
	s := benchSeer(b, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out string
		if err := s.Get("doc0").Get("meta").Get("id").Value(&out); err != nil {
			b.Fatal(err)
		}
	}
}

// Set + Commit a nested field on a cached document.
func BenchmarkSetCommit(b *testing.B) {
	s := benchSeer(b, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Get("doc0").Document().Get("meta").Get("id").Set("x").Commit(); err != nil {
			b.Fatal(err)
		}
	}
}

// List keys of a mapping node (schema enumerates children constantly).
func BenchmarkList(b *testing.B) {
	s := benchSeer(b, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Get("doc0").Get("meta").List(); err != nil {
			b.Fatal(err)
		}
	}
}

// Sync with one document changed out of 32 cached — the realistic case
// (a few writes against a large loaded config). Should touch only the
// dirty doc, not re-encode the other 31.
func BenchmarkSyncOneDirty(b *testing.B) {
	s := benchSeer(b, 32)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Get("doc0").Document().Get("meta").Get("id").Set("x").Commit(); err != nil {
			b.Fatal(err)
		}
		if err := s.Sync(); err != nil {
			b.Fatal(err)
		}
	}
}
