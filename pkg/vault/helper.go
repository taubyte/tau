package vault

import _ "embed"

var (
	//go:embed helper/secret-x86_64
	helperAMD64 []byte

	//go:embed helper/secret-aarch64
	helperARM64 []byte
)

// helperFor is the helper built for a GOARCH, or nil.
func helperFor(arch string) []byte {
	switch arch {
	case "amd64":
		return helperAMD64
	case "arm64":
		return helperARM64
	}
	return nil
}
