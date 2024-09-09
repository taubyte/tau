package common

const (
	// 0755 owner can read/write/execute, group/others can read/execute.
	DefaultDirPermission = 0755

	// 0644 owner can read/write, group/others can read only
	DefaultFilePermission = 0644
)

var (
	HTTPMethodTypes = []string{"GET", "POST", "PUT", "DELETE", "CONNECT", "HEAD", "OPTIONS", "TRACE", "PATH"}
	SizeUnitTypes   = []string{"KB", "MB", "GB", "TB", "PB"}
)
