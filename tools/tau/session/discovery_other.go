//go:build !windows

package session

// getParentPIDNative is only implemented on Windows (NtQueryInformationProcess).
// On other platforms we rely on go-ps for the process tree.
func getParentPIDNative(_ int) (int, bool) {
	return 0, false
}

// getProcessNameNative is only implemented on Windows (QueryFullProcessImageNameW).
// On other platforms we rely on go-ps for the executable name.
func getProcessNameNative(_ int) string {
	return ""
}

// debugProcessTreeNative prints up to 20 parents using OS-native APIs (Windows: NtQueryInformationProcess).
// On non-Windows we only log that it's skipped so the code path is visible.
func debugProcessTreeNative(_ int) {
	debugSession("--- process tree (native): skipped (Windows only) ---")
}
