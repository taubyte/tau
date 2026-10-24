//go:build !linux && !darwin && !windows

package reserve

// measured is a filesystem this cannot read, which reports no totals and so is
// held to no reserve.
func measured(string) (Usage, error) {
	return Usage{}, nil
}
