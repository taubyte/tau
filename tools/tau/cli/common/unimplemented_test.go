package common

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestUnimplementedBasic_AllCommandsReturnNil(t *testing.T) {
	var u UnimplementedBasic
	cmd, opts := u.Base()
	assert.Assert(t, cmd == nil)
	assert.Assert(t, opts == nil)
	assert.Assert(t, u.New() == nil)
	assert.Assert(t, u.Edit() == nil)
	assert.Assert(t, u.Delete() == nil)
	assert.Assert(t, u.Query() == nil)
	assert.Assert(t, u.List() == nil)
	assert.Assert(t, u.Select() == nil)
	assert.Assert(t, u.Clone() == nil)
	assert.Assert(t, u.Push() == nil)
	assert.Assert(t, u.Pull() == nil)
	assert.Assert(t, u.Checkout() == nil)
	assert.Assert(t, u.Import() == nil)
}

func TestNotImplementedIsNil(t *testing.T) {
	assert.Assert(t, NotImplemented == nil)
}
