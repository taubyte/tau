package poe

import (
	"embed"
	"io/fs"
	"testing"
	"testing/fstest"

	"gotest.tools/v3/assert"
)

//go:embed testdata/*
var testFilesEmbed embed.FS
var testFiles, _ = fs.Sub(testFilesEmbed, "testdata")

func TestNew(t *testing.T) {
	engine, err := New(testFiles, "score.star")
	assert.NilError(t, err, "New() should not return an error")
	assert.Assert(t, engine != nil, "engine should not be nil")
}

func TestScore(t *testing.T) {
	engine, err := New(testFiles, "score.star")
	assert.NilError(t, err, "New() should not return an error")

	score, err := engine.Score("test", map[string]any{})
	assert.NilError(t, err, "Score() should not return an error")
	// "test" has length 4, normalized: 4/10 = 0.4
	assert.Equal(t, score, 0.4, "Score should be 0.4 for target 'test'")
}

func TestCheck(t *testing.T) {
	engine, err := New(testFiles, "check.star")
	assert.NilError(t, err, "New() should not return an error")

	check, err := engine.Check("test", map[string]any{})
	assert.NilError(t, err, "Check() should not return an error")
	assert.Assert(t, check, "Check should return true for non-empty target")
}

func TestScoreAndCheck(t *testing.T) {
	engine, err := New(testFiles, "score_and_check.star")
	assert.NilError(t, err, "New() should not return an error")

	score, err := engine.Score("test", map[string]any{"multiplier": 2})
	assert.NilError(t, err, "Score() should not return an error")
	// "test" has length 4, base: 4/10 = 0.4, with multiplier 2: 0.4 * 2/2 = 0.4
	assert.Equal(t, score, 0.4, "Score should be 0.4 with multiplier 2")

	check, err := engine.Check("test", map[string]any{"required": true})
	assert.NilError(t, err, "Check() should not return an error")
	assert.Assert(t, check, "Check should return true")
}

func TestScoreWithMissingFile(t *testing.T) {
	engine, err := New(testFiles, "missing.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Score("test", map[string]any{})
	assert.ErrorContains(t, err, "failed to load module")
}

func TestCheckWithMissingFile(t *testing.T) {
	engine, err := New(testFiles, "missing.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Check("test", map[string]any{})
	assert.ErrorContains(t, err, "failed to load module")
}

func TestScoreWithNoScoreFunction(t *testing.T) {
	engine, err := New(testFiles, "check.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Score("test", map[string]any{})
	assert.ErrorContains(t, err, "score")
}

func TestCheckWithNoCheckFunction(t *testing.T) {
	engine, err := New(testFiles, "score.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Check("test", map[string]any{})
	assert.ErrorContains(t, err, "check")
}

func TestScoreWithImport(t *testing.T) {
	engine, err := New(testFiles, "score_with_import.star")
	assert.NilError(t, err, "New() should not return an error")

	score, err := engine.Score("test", map[string]any{})
	assert.NilError(t, err, "Score() should not return an error when using imports")
	assert.Equal(t, score, 0.9, "Score should be 0.9 for target 'test' with import")
}

func TestCheckWithEmptyTarget(t *testing.T) {
	engine, err := New(testFiles, "check.star")
	assert.NilError(t, err, "New() should not return an error")

	check, err := engine.Check("", map[string]any{})
	assert.NilError(t, err, "Check() should not return an error")
	assert.Assert(t, !check, "Check should return false for empty target")
}

func TestScoreWithMissingImport(t *testing.T) {
	missingImportFS := fstest.MapFS{
		"bad_import.star": &fstest.MapFile{
			Data: []byte(`load("nonexistent.star", "func")
def score(target, data):
    return 1.0`),
		},
	}

	engine, err := New(missingImportFS, "bad_import.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Score("test", map[string]any{})
	assert.ErrorContains(t, err, "failed to load module")
}

func TestScoreWithWrongReturnType(t *testing.T) {
	engine, err := New(testFiles, "wrong_score_type.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Score("test", map[string]any{})
	assert.ErrorContains(t, err, "score function did not return a float64")
}

func TestCheckWithWrongReturnType(t *testing.T) {
	engine, err := New(testFiles, "wrong_check_type.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Check("test", map[string]any{})
	assert.ErrorContains(t, err, "check function did not return a bool")
}

func TestScoreWithNegativeValue(t *testing.T) {
	negativeScoreFS := fstest.MapFS{
		"negative_score.star": &fstest.MapFile{
			Data: []byte(`def score(target, data):
    return -0.5`),
		},
	}

	engine, err := New(negativeScoreFS, "negative_score.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Score("test", map[string]any{})
	assert.ErrorContains(t, err, "score must be between 0 and 1")
}

func TestScoreWithValueGreaterThanOne(t *testing.T) {
	highScoreFS := fstest.MapFS{
		"high_score.star": &fstest.MapFile{
			Data: []byte(`def score(target, data):
    return 1.5`),
		},
	}

	engine, err := New(highScoreFS, "high_score.star")
	assert.NilError(t, err, "New() should not return an error")

	_, err = engine.Score("test", map[string]any{})
	assert.ErrorContains(t, err, "score must be between 0 and 1")
}

func TestScoreWithValidBoundaryValues(t *testing.T) {
	boundaryScoreFS := fstest.MapFS{
		"boundary_score.star": &fstest.MapFile{
			Data: []byte(`def score(target, data):
    if target == "zero":
        return 0.0
    elif target == "one":
        return 1.0
    else:
        return 0.5`),
		},
	}

	engine, err := New(boundaryScoreFS, "boundary_score.star")
	assert.NilError(t, err, "New() should not return an error")

	// Test score of 0.0 (valid)
	score, err := engine.Score("zero", map[string]any{})
	assert.NilError(t, err, "Score() should not return an error for 0.0")
	assert.Equal(t, score, 0.0, "Score should be 0.0")

	// Test score of 1.0 (valid)
	score, err = engine.Score("one", map[string]any{})
	assert.NilError(t, err, "Score() should not return an error for 1.0")
	assert.Equal(t, score, 1.0, "Score should be 1.0")

	// Test score between 0 and 1 (valid)
	score, err = engine.Score("middle", map[string]any{})
	assert.NilError(t, err, "Score() should not return an error for 0.5")
	assert.Equal(t, score, 0.5, "Score should be 0.5")
}
