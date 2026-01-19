package jobs

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/taubyte/tau/core/common"
	"github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	ci "github.com/taubyte/tau/pkg/containers"
)

type Context struct {
	ctx              context.Context
	ctxC             context.CancelFunc
	Node             peer.Node
	Tns              tns.Client
	RepoType         common.RepositoryType
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
	case common.ConfigRepository:
		return &config{c}, nil
	case common.CodeRepository:
		return &code{c}, nil
	case common.LibraryRepository:
		return &library{c}, nil
	case common.WebsiteRepository:
		return &website{c}, nil
	default:
		return nil, fmt.Errorf("unexpected repository type `%d`", c.RepoType)
	}
}
