package lib

//go:wasm-module testing
//export getFloat64
func getFloat64(val uint32) float64

//export ping
func ping(val uint32) uint32 {
	// Pre-fix (#437) the satellite plugin decodes an f64 host return with the
	// wrong reflect case, so the guest never gets 5.5 back. Returns 1 only when
	// the float64 round-trips intact.
	if getFloat64(val) != float64(val)+0.5 {
		return 0
	}
	return 1
}
