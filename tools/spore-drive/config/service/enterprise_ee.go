//go:build ee

package service

import "github.com/taubyte/tau/ee"

// Mount the ee spore-drive config handler onto the shared config mux. All ee
// logic (the connect handler, the ee proto, the reflective op walk) lives in the
// ee submodule; this seam only registers the generic mount. Community builds
// exclude it and eeConfigHandlers stays empty.
func init() {
	eeConfigHandlers = append(eeConfigHandlers, ee.SporeDriveConfigHandler)
}
