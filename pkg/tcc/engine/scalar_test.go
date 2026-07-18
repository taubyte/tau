package engine

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"gotest.tools/v3/assert"
)

func TestParseDuration_Success(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", "5s"))

	assert.NilError(t, parseDuration(sel, "timeout"))

	v, err := sel.Get("timeout")
	assert.NilError(t, err)
	assert.Equal(t, v.(int64), int64(5000000000))
}

func TestParseDuration_VariousDurations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration string
		expected int64
	}{
		{"1 hour", "1h", 3600000000000},
		{"30 minutes", "30m", 1800000000000},
		{"2 seconds", "2s", 2000000000},
		{"500 milliseconds", "500ms", 500000000},
		{"1 hour 30 minutes", "1h30m", 5400000000000},
		{"1.5 seconds", "1.5s", 1500000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := object.New[object.Refrence]().Child("test")
			sel.Set("timeout", tc.duration)
			assert.NilError(t, parseDuration(sel, "timeout"))
			v, err := sel.Get("timeout")
			assert.NilError(t, err)
			assert.Equal(t, v.(int64), tc.expected)
		})
	}
}

func TestParseDuration_MissingField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, parseDuration(sel, "nonexistent"))
}

func TestParseDuration_NilField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", nil))
	assert.NilError(t, parseDuration(sel, "timeout"))
}

func TestParseDuration_InvalidType(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", 123))
	assert.ErrorContains(t, parseDuration(sel, "timeout"), "timeout is not a string")
}

func TestParseDuration_InvalidDuration(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", "invalid-duration"))
	assert.ErrorContains(t, parseDuration(sel, "timeout"), "parsing timeout failed")
}

func TestParseBytes_Success(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", "128MB"))

	assert.NilError(t, parseBytes(sel, "memory"))

	v, err := sel.Get("memory")
	assert.NilError(t, err)
	assert.Equal(t, v.(int64), int64(128000000))
}

func TestParseBytes_VariousSizes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     string
		expected int64
	}{
		{"1 GB", "1GB", 1000000000},
		{"512 MB", "512MB", 512000000},
		{"2 KB", "2KB", 2000},
		{"1 TB", "1TB", 1000000000000},
		{"256 bytes", "256B", 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := object.New[object.Refrence]().Child("test")
			sel.Set("memory", tc.size)
			assert.NilError(t, parseBytes(sel, "memory"))
			v, err := sel.Get("memory")
			assert.NilError(t, err)
			assert.Equal(t, v.(int64), tc.expected)
		})
	}
}

func TestParseBytes_MissingField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, parseBytes(sel, "nonexistent"))
}

func TestParseBytes_NilField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", nil))
	assert.NilError(t, parseBytes(sel, "memory"))
}

func TestParseBytes_InvalidType(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", 123))
	assert.ErrorContains(t, parseBytes(sel, "memory"), "memory is not a string")
}

func TestParseBytes_InvalidSize(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", "invalid-size"))
	assert.ErrorContains(t, parseBytes(sel, "memory"), "parsing memory failed")
}

func TestFormatDuration_Success(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", int64(5000000000)))

	assert.NilError(t, formatDuration(sel, "timeout"))

	v, err := sel.Get("timeout")
	assert.NilError(t, err)
	assert.Equal(t, v.(string), "5s")
}

func TestFormatDuration_VariousTypes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		value    interface{}
		expected string
	}{
		{"int64", int64(3600000000000), "1h0m0s"},
		{"int", int(1800000000000), "30m0s"},
		{"int32", int32(2000000000), "2s"},
		{"500ms", int64(500000000), "500ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := object.New[object.Refrence]().Child("test")
			sel.Set("timeout", tc.value)
			assert.NilError(t, formatDuration(sel, "timeout"))
			v, err := sel.Get("timeout")
			assert.NilError(t, err)
			assert.Equal(t, v.(string), tc.expected)
		})
	}
}

func TestFormatDuration_MissingField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, formatDuration(sel, "nonexistent"))
}

func TestFormatDuration_NilField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", nil))
	assert.NilError(t, formatDuration(sel, "timeout"))
}

func TestFormatDuration_InvalidType(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("timeout", "5s"))
	assert.ErrorContains(t, formatDuration(sel, "timeout"), "timeout is not an integer")
}

func TestFormatBytes_Success(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", int64(128000000)))

	assert.NilError(t, formatBytes(sel, "memory"))

	v, err := sel.Get("memory")
	assert.NilError(t, err)
	assert.Assert(t, v.(string) != "", "memory should be formatted as string")
}

func TestFormatBytes_VariousTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value interface{}
	}{
		{"int64", int64(1000000000)},
		{"int", int(512000000)},
		{"int32", int32(2000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sel := object.New[object.Refrence]().Child("test")
			sel.Set("memory", tc.value)
			assert.NilError(t, formatBytes(sel, "memory"))
			v, err := sel.Get("memory")
			assert.NilError(t, err)
			assert.Assert(t, v.(string) != "", "memory should be formatted as string")
		})
	}
}

func TestFormatBytes_MissingField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, formatBytes(sel, "nonexistent"))
}

func TestFormatBytes_NilField(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", nil))
	assert.NilError(t, formatBytes(sel, "memory"))
}

func TestFormatBytes_InvalidType(t *testing.T) {
	sel := object.New[object.Refrence]().Child("test")
	assert.NilError(t, sel.Set("memory", "128MB"))
	assert.ErrorContains(t, formatBytes(sel, "memory"), "memory is not an integer")
}
