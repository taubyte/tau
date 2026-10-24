package reserve

import "golang.org/x/sys/unix"

// measured reads the filesystem holding path. Its blocks are counted in
// fragments, which older kernels leave unreported and are then its blocks.
func measured(path string) (Usage, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Usage{}, err
	}
	unit := uint64(st.Frsize)
	if unit == 0 {
		unit = uint64(st.Bsize)
	}
	return Usage{
		Size:      st.Blocks * unit,
		Free:      st.Bavail * unit,
		Files:     st.Files,
		FreeFiles: st.Ffree,
	}, nil
}
