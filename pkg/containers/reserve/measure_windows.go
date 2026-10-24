package reserve

import "golang.org/x/sys/windows"

// measured reads the volume holding path. NTFS has no fixed count of files, so
// there are no inodes to hold back.
func measured(path string) (Usage, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Usage{}, err
	}
	var free, size, all uint64
	if err := windows.GetDiskFreeSpaceEx(name, &free, &size, &all); err != nil {
		return Usage{}, err
	}
	return Usage{Size: size, Free: free}, nil
}
