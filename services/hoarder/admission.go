//go:build !ee

package hoarder

// Write-admission seam, wired into the put/batch handlers so an admission
// policy can reject a write (CodeOverCapacity) without touching the wire
// format. This build admits every write.

func (srv *Service) admitWrite(project string, size int) error { return nil }
