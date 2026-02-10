//go:build windows

package session

import (
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	processBasicInformation = 0
)

// processBasicInformationStruct matches PROCESS_BASIC_INFORMATION (InheritedFromUniqueProcessId = parent PID).
type processBasicInformationStruct struct {
	Reserved1                    uintptr
	PebBaseAddress               uintptr
	Reserved2                    [2]uintptr
	UniqueProcessID              uintptr
	InheritedFromUniqueProcessID uintptr // parent process ID
}

var (
	ntdll                          = windows.NewLazySystemDLL("ntdll.dll")
	procNtQueryInformationProcess  = ntdll.NewProc("NtQueryInformationProcess")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

// getParentPIDNative returns the parent process ID for the given pid using the Windows
// native API (NtQueryInformationProcess). This can resolve parents that go-ps cannot
// see (e.g. Cygwin/MSYS2/Git Bash parent of sh). Returns (parentPid, true) or (0, false).
func getParentPIDNative(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		debugSession("getParentPIDNative: OpenProcess(pid=%d) err=%v", pid, err)
		return 0, false
	}
	defer windows.CloseHandle(h)

	var info processBasicInformationStruct
	status, _, _ := procNtQueryInformationProcess.Call(
		uintptr(h),
		uintptr(processBasicInformation),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		0,
	)
	if status != 0 {
		// STATUS_SUCCESS is 0. Other values (e.g. 0xC0000005 = access denied) mean failure.
		debugSession("getParentPIDNative: NtQueryInformationProcess(pid=%d) status=0x%X", pid, status)
		return 0, false
	}
	parent := int(info.InheritedFromUniqueProcessID)
	return parent, parent > 0
}

// getProcessNameNative returns the process image name (e.g. "bash.exe") for pid using
// QueryFullProcessImageNameW, so we get a real name even when go-ps cannot see the process.
func getProcessNameNative(pid int) string {
	if pid <= 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 512)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(
		uintptr(h),
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r == 0 {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

const debugProcessTreeNativeMaxDepth = 20

// debugProcessTreeNative prints up to 20 parents using NtQueryInformationProcess so we see
// the full chain (including processes go-ps cannot see) with native-resolved exe names.
func debugProcessTreeNative(startPid int) {
	debugSession("--- process tree (native), start pid=%d, max depth %d ---", startPid, debugProcessTreeNativeMaxDepth)
	pid := startPid
	for depth := 0; depth < debugProcessTreeNativeMaxDepth && pid > 0 && pid != 1; depth++ {
		exe := processName(pid)
		debugSession("  [%2d] pid=%d exe=%q", depth, pid, exe)
		next, ok := getParentPIDNative(pid)
		if !ok || next == 0 || next == 1 {
			if next > 0 {
				debugSession("  [%2d] pid=%d exe=%q (parent; OpenProcess failed for next)", depth+1, next, processName(next))
			}
			break
		}
		pid = next
	}
	debugSession("--- end process tree (native) ---")
}
