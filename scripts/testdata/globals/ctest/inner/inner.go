// Package inner sits under test support, and is test support too.
package inner

type reg struct{}

func newReg() *reg { return &reg{} }

var r = newReg()
