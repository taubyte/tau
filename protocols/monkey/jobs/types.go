package jobs

import (
	"context"
	"fmt"
	"os"

	compilerCommon "github.com/taubyte/config-compiler/common"
	"github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/tns"
	ci "github.com/taubyte/go-simple-container"
	"github.com/taubyte/p2p/peer"
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

	OdoClientNode peer.Node

	DVPublicKey []byte
}

type Op struct {
	id           string
	name         string
	application  string
	pathVariable string
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
