package resources_test

import (
	"testing"

	res "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"gotest.tools/v3/assert"
)

func TestPanicIfMissingValue_NilFunc(t *testing.T) {
	type handler struct {
		Do func()
	}
	h := &handler{} // Do is nil
	assert.Assert(t, panics(t, func() { res.PanicIfMissingValue(h) }))
}

func TestPanicIfMissingValue_EmptyString(t *testing.T) {
	type handler struct {
		Name string
	}
	h := &handler{} // Name is ""
	assert.Assert(t, panics(t, func() { res.PanicIfMissingValue(h) }))
}

func TestPanicIfMissingValue_Valid(t *testing.T) {
	type handler struct {
		Do   func()
		Name string
	}
	h := &handler{
		Do:   func() {},
		Name: "ok",
	}
	assert.Assert(t, !panics(t, func() { res.PanicIfMissingValue(h) }))
}

func panics(t *testing.T, f func()) (ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			ok = true
		}
	}()
	f()
	return false
}
