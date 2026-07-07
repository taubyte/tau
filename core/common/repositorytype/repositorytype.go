// Package repositorytype defines the repository-kind enum shared between the
// config compiler, the tcc pipeline, and monkey. It lives in its own leaf
// package (importing nothing) so the tcc compile path can use it without
// dragging in core/common's libp2p/seer dependencies — which matters for the
// GOOS=js wasm build.
package repositorytype

type Type int

const (
	UnknownRepository Type = iota
	ConfigRepository
	CodeRepository
	LibraryRepository
	WebsiteRepository
)
