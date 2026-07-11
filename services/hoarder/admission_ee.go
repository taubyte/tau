//go:build ee

package hoarder

// Write-admission seam (see admission.go). This build admits every write.
func (srv *Service) admitWrite(project string, size int) error { return nil }
