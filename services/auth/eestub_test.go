//go:build !ee

package auth

// eeStub is empty in the community build — there are no ee-only Client methods to fill.
type eeStub = struct{}
