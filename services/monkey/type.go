package monkey

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sync"
	"time"

	patrickClient "github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/config"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	iface "github.com/taubyte/tau/core/services/monkey"
	"github.com/taubyte/tau/core/services/patrick"
	tnsClient "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
)

// TODO: This sucks
/* This is a variable so that it can be overridden in tests */
var NewPatrick = func(ctx context.Context, node peer.Node) (patrick.Client, error) {
	return patrickClient.New(ctx, node)
}

var _ iface.Service = &Service{}

type Monkey struct {
	ctx     context.Context
	ctxC    context.CancelFunc
	Id      string
	Status  patrick.JobStatus
	LogCID  string
	Service *Service
	Job     *patrick.Job
	logFile *os.File

	start time.Time

	generatedDomainRegExp *regexp.Regexp
}

type Service struct {
	ctx    context.Context
	node   peer.Node
	stream streams.CommandService

	patrickClient patrick.Client
	tnsClient     tnsClient.Client
	clientNode    peer.Node
	hoarderClient hoarderIface.Client

	config *config.Node

	recvJobs     map[string]time.Time
	recvJobsLock sync.RWMutex

	monkeys     map[string]*Monkey
	monkeysLock sync.RWMutex

	dev         bool
	dvPublicKey []byte
}

func (s *Service) Hoarder() hoarderIface.Client {
	return s.hoarderClient
}

func (s *Service) Patrick() patrick.Client {
	return s.patrickClient
}

func (s *Service) Delete(jid string) {
	s.monkeysLock.Lock()
	defer s.monkeysLock.Unlock()
	delete(s.monkeys, jid)
}

func (s *Service) Node() peer.Node {
	return s.node
}

func (s *Service) Dev() bool {
	return s.dev
}

type Config config.Node

func appendAndLogError(e chan error, format string, args ...any) {
	ferr := fmt.Errorf(format, args...)
	logger.Error(ferr)
	e <- ferr
}
