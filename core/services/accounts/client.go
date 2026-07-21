//go:build !ee

package accounts

// eeSurface adds no methods in the community build — linkage is the whole
// access model. The ee build aliases the real surface (client_ee.go).
type eeSurface = any
