package common

import "strings"

// Source represents `inline`, `.` or a library name
type Source string

func (s Source) Inline() bool {
	return len(s) > 0 && !strings.HasPrefix(s.String(), SourceLibraryPrefix)
}

func (s Source) String() string {
	return string(s)
}

const (
	FunctionTypeHttp           = "http"
	FunctionTypeHttps          = "https"
	FunctionTypeP2P            = "p2p"
	FunctionTypePubSub         = "pubsub"
	DefaultGeneratedDomainName = "generated"
	DefaultNewProjectBranch    = "main"

	SourceLibraryPrefix = "library"

	ConfigRepoPrefix  = "tb_%s"
	CodeRepoPrefix    = "tb_code_%s"
	WebsiteRepoPrefix = "tb_website_%s"
	LibraryRepoPrefix = "tb_library_%s"

	ConfigRepoDir  = "config"
	CodeRepoDir    = "code"
	WebsiteRepoDir = "websites"
	LibraryRepoDir = "libraries"

	SelectionInline = Source(".")
)

var (
	FunctionTypes = []string{FunctionTypeHttp, FunctionTypeHttps, FunctionTypeP2P, FunctionTypePubSub}
	BucketTypes   = []string{"Object", "Streaming"}
)
