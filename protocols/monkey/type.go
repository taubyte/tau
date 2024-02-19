package monkey

import (
	"context"
	"errors"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	iface "github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
	tnsClient "github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"
	"github.com/taubyte/tau/clients/p2p/hoarder"
	patrickClient "github.com/taubyte/tau/clients/p2p/patrick"
	"github.com/taubyte/tau/config"
	tauConfig "github.com/taubyte/tau/config"
	chidori "github.com/taubyte/utils/logger/zap"
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
	stream *streams.CommandService

	patrickClient patrick.Client
	tnsClient     tnsClient.Client
	odoClientNode peer.Node
	hoarderClient *hoarder.Client

	config *tauConfig.Node

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

func appendAndLog(e []error, format string, args ...any) {
	if errString := chidori.Format(logger, log.LevelError, format, args...); len(errString) > 0 {
		err := errors.New(errString)

		e = append(e, err)
	}
}
