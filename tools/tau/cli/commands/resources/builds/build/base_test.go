package build

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestNew(t *testing.T) {
	b := New()
	assert.Assert(t, b != nil)
}

func TestLink_Base(t *testing.T) {
	var l link
	cmd, _ := l.Base()
	assert.Assert(t, cmd != nil)
	assert.Equal(t, cmd.Name, "build")
}

func TestLink_Query(t *testing.T) {
	var l link
	q := l.Query()
	assert.Assert(t, q != nil)
}

func TestLink_Cancel(t *testing.T) {
	var l link
	c := l.Cancel()
	assert.Assert(t, c != nil)
}

func TestLink_Retry(t *testing.T) {
	var l link
	r := l.Retry()
	assert.Assert(t, r != nil)
}
