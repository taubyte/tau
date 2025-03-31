package jobs

import (
	"context"
	"fmt"
	"os"
	"regexp"

	ci "github.com/taubyte/go-simple-container"
	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	compilerCommon "github.com/taubyte/tau/pkg/config-compiler/common"
)

type Context struct {
	ctx              context.Context
	ctxC             context.CancelFunc
	Node             peer.Node
	Tns              tns.Client
	RepoType         compilerCommon.RepositoryType
	ProjectID        string
	DeployKey        string
	Job              *patrick.Job
	LogFile          *os.File
	gitDir           string
	WorkDir          string
	Patrick          patrick.Client
	ContainerClient  *ci.Client
	ConfigRepoId     int
	ConfigPrivateKey string
	ConfigRepoRoot   string
	Monkey           monkey.Service

	GeneratedDomainRegExp *regexp.Regexp

	ClientNode peer.Node

	DVPublicKey []byte
}

type Op struct {
	id           string
	name         string
	application  string
	pathVariable string
	err          error
}

type code struct{ Context }
type website struct{ Context }
type library struct{ Context }
type config struct{ Context }

type repo interface {
	handle() error
}

func (c Context) Handler() (repo, error) {
	switch c.RepoType {
	case compilerCommon.ConfigRepository:
		return &config{c}, nil
	case compilerCommon.CodeRepository:
		return &code{c}, nil
	case compilerCommon.LibraryRepository:
		return &library{c}, nil
	case compilerCommon.WebsiteRepository:
		return &website{c}, nil
	default:
		return nil, fmt.Errorf("unexpected repository type `%d`", c.RepoType)
	}
}
