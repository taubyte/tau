package poe

import (
	"embed"
	"io/fs"
	"testing"
)

//go:embed testdata/*
var benchmarkFilesEmbed embed.FS
var benchmarkFiles, _ = fs.Sub(benchmarkFilesEmbed, "testdata")

var (
	benchmarkEngine Engine
	benchmarkScore  float64
	benchmarkCheck  bool
)

func BenchmarkNew(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		engine, err := New(benchmarkFiles, "score.star")
		if err != nil {
			b.Fatalf("failed to create engine: %v", err)
		}
		benchmarkEngine = engine
	}
}

func BenchmarkScore(b *testing.B) {
	engine, err := New(benchmarkFiles, "score.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	data := map[string]any{}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		score, err := engine.Score("test", data)
		if err != nil {
			b.Fatalf("score failed: %v", err)
		}
		benchmarkScore = score
	}
}

func BenchmarkScoreWithData(b *testing.B) {
	engine, err := New(benchmarkFiles, "score_and_check.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	data := map[string]any{
		"multiplier": 2,
		"factor":     1.5,
		"enabled":    true,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		score, err := engine.Score("test_target", data)
		if err != nil {
			b.Fatalf("score failed: %v", err)
		}
		benchmarkScore = score
	}
}

func BenchmarkCheck(b *testing.B) {
	engine, err := New(benchmarkFiles, "check.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		check, err := engine.Check("test", map[string]any{})
		if err != nil {
			b.Fatalf("check failed: %v", err)
		}
		benchmarkCheck = check
	}
}

func BenchmarkCheckWithData(b *testing.B) {
	engine, err := New(benchmarkFiles, "score_and_check.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	data := map[string]any{
		"required": true,
		"valid":    true,
		"count":    10,
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		check, err := engine.Check("test_target", data)
		if err != nil {
			b.Fatalf("check failed: %v", err)
		}
		benchmarkCheck = check
	}
}

func BenchmarkScoreWithImport(b *testing.B) {
	engine, err := New(benchmarkFiles, "score_with_import.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		score, err := engine.Score("test", map[string]any{})
		if err != nil {
			b.Fatalf("score failed: %v", err)
		}
		benchmarkScore = score
	}
}

func BenchmarkScoreParallel(b *testing.B) {
	engine, err := New(benchmarkFiles, "score.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	data := map[string]any{}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			score, err := engine.Score("test", data)
			if err != nil {
				b.Fatalf("score failed: %v", err)
			}
			benchmarkScore = score
		}
	})
}

func BenchmarkCheckParallel(b *testing.B) {
	engine, err := New(benchmarkFiles, "check.star")
	if err != nil {
		b.Fatalf("failed to create engine: %v", err)
	}

	data := map[string]any{}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			check, err := engine.Check("test", data)
			if err != nil {
				b.Fatalf("check failed: %v", err)
			}
			benchmarkCheck = check
		}
	})
}
