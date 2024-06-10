package p2p

import (
	"context"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/taubyte/tau/core/services/substrate/components"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Command interface {
	Send(ctx context.Context, body map[string]interface{}) (response.Response, error)
	SendTo(ctx context.Context, cid cid.Cid, body map[string]interface{}) (response.Response, error)
}

type Stream interface {
	Listen() (protocol string, err error)
	Command(command string) (Command, error)
	Close()
}

type StreamHandler func(cmd *command.Command) (resp response.Response, err error)

type CommandService interface {
	Close()
}

type MatchDefinition struct {
	Project     string
	Application string
	Protocol    string
	Command     string
}

func (m *MatchDefinition) String() string {
	return m.Project + m.Application + m.Protocol + m.Command
}

func (m *MatchDefinition) CachePrefix() string {
	return m.Project
}

type Service interface {
	components.ServiceComponent
	Stream(ctx context.Context, projectID, applicationID, protocol string) (Stream, error)
	StartStream(name, protocol string, handler StreamHandler) (CommandService, error)
	LookupService(matcher *MatchDefinition) (config *structureSpec.Service, application string, err error)
	Discover(ctx context.Context, max int, timeout time.Duration) ([]peer.AddrInfo, error)
}

type Serviceable interface {
	components.FunctionServiceable
	Handle(data *command.Command) (time.Time, response.Response, error)
	Name() string
	Close()
}

type ServiceResource interface {
	Application() string
	Config() *structureSpec.Service
	Context() context.Context
	SmartOps(smartOps []string) (uint32, error)
	Type() uint32
}
