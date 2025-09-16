package monkey

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path"
	"regexp"
	"sync"
	"time"

	"github.com/ipfs/go-log/v2"
	ci "github.com/taubyte/go-simple-container/gc"
	"github.com/taubyte/tau/clients/p2p/hoarder"
	patrickClient "github.com/taubyte/tau/clients/p2p/patrick"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	iface "github.com/taubyte/tau/core/services/monkey"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	seerIface "github.com/taubyte/tau/core/services/seer"
	tnsIface "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"
	protocolCommon "github.com/taubyte/tau/services/common"

	"github.com/taubyte/tau/services/monkey/worker"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

var logger = log.Logger("tau.monkey.service")

// TODO: This sucks
/* This is a variable so that it can be overridden in tests */
var NewPatrick = func(ctx context.Context, node peer.Node) (patrickIface.Client, error) {
	return patrickClient.New(ctx, node)
}

var _ iface.Service = &Service{}

type Worker struct {
	ctx     context.Context
	ctxC    context.CancelFunc
	Id      string
	Status  patrickIface.JobStatus
	LogCID  string
	Service *Service
	Job     *patrickIface.Job
	logFile *os.File

	start time.Time

	generatedDomainRegExp *regexp.Regexp
}

type Service struct {
	ctx    context.Context
	node   peer.Node
	stream streams.CommandService

	patrickClient patrickIface.Client
	tnsClient     tnsIface.Client
	clientNode    peer.Node
	hoarderClient hoarderIface.Client

	config *tauConfig.Node

	monkeys     map[string]*worker
	monkeysLock sync.RWMutex

	dev         bool
	dvPublicKey []byte
}

func (s *Service) Hoarder() hoarderIface.Client {
	return s.hoarderClient
}

func (s *Service) Patrick() patrickIface.Client {
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

type Config tauConfig.Node

func appendAndLogError(e chan error, format string, args ...any) {
	ferr := fmt.Errorf(format, args...)
	logger.Error(ferr)
	e <- ferr
}

func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		patrickSpecs.PubSubIdent,
		func(msg *pubsub.Message) {
			go srv.pubsubMsgHandler(msg)
		},
		func(err error) {
			if err.Error() != "context canceled" {
				logger.Error("Subscription had an error:", err.Error())
				if err := srv.subscribe(); err != nil {
					logger.Error("resubscribe failed with:", err.Error())
				}
			}
		},
	)
}

func New(ctx context.Context, config *tauConfig.Node) (*Service, error) {
	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	srv := &Service{
		ctx:    ctx,
		dev:    config.DevMode,
		config: config,
	}

	err = ci.Start(ctx, ci.Interval(ci.DefaultInterval), ci.MaxAge(ci.DefaultMaxAge))
	if err != nil {
		return nil, err
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, config, path.Join(config.Root, protocolCommon.Monkey))
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = config.Node
		srv.dvPublicKey = config.DomainValidation.PublicKey
	}

	srv.clientNode = srv.node
	if config.ClientNode != nil {
		srv.clientNode = config.ClientNode
	}

	err = srv.subscribe()
	if err != nil {
		return nil, err
	}

	srv.stream, err = streams.New(srv.node, protocolCommon.Monkey, protocolCommon.MonkeyProtocol)
	if err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()

	sc, err := seerClient.New(ctx, srv.clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = protocolCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeMonkey)
	if err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with %s", err)
	}

	srv.monkeys = make(map[string]*worker, 0)

	srv.patrickClient, err = NewPatrick(ctx, srv.clientNode)
	if err != nil {
		return nil, err
	}

	srv.tnsClient, err = tnsClient.New(ctx, srv.clientNode)
	if err != nil {
		return nil, err
	}

	srv.hoarderClient, err = hoarder.New(ctx, srv.clientNode)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Monkey)
	defer logger.Info(protocolCommon.Monkey, "closed")

	srv.stream.Stop()

	srv.tnsClient.Close()
	srv.patrickClient.Close()

	return nil
}

func (s *Service) newMonkey(job *patrickIface.Job) (*worker, error) {
	jid := job.Id
	err := s.patrickClient.Lock(jid, uint32(protocolCommon.DefaultLockTime/time.Second))
	if err != nil {
		return nil, err
	}

	var locked bool
	for i := 0; i < protocolCommon.DefaultLockCheckAttempts; i++ {
		s.randSleep()

		locked, err = s.patrickClient.IsLocked(jid)
		if locked {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("checking if job %s is locked failed with: %w", jid, err)
	}

	if !locked {
		return nil, fmt.Errorf("job %s not locked", jid)
	}

	logFile, err := s.createTempLogFile(jid)
	if err != nil {
		return nil, err
	}

	m := &worker{
		Id:                    jid,
		Status:                patrickIface.JobStatusOpen,
		Service:               s,
		Job:                   job,
		logFile:               logFile,
		generatedDomainRegExp: s.config.GeneratedDomainRegExp,
		start:                 time.Now(),
	}

	m.ctx, m.ctxC = context.WithCancel(s.ctx)

	s.monkeysLock.Lock()
	s.monkeys[jid] = m
	s.monkeysLock.Unlock()

	return m, nil
}

func (s *Service) randSleep() {
	var coefficient float64 = 1

	n, err := rand.Int(rand.Reader, big.NewInt(1<<53))
	if err == nil {
		coefficient += float64(n.Int64()) / float64(1<<53)
	}

	select {
	case <-s.ctx.Done():
		return
	case <-time.After(time.Duration(coefficient/float64(protocolCommon.DefaultLockCheckAttempts)) * protocolCommon.DefaultLockMinWaitTime):
		return
	}
}

func (s *Service) createTempLogFile(jid string) (*os.File, error) {
	return os.CreateTemp("/tmp", fmt.Sprintf("log-%s", jid))
}

type workerNode struct {
	tnsClient     tnsIface.Client
	patrickClient patrickIface.Client
	hoarderClient hoarderIface.Client
	peer.Node
}

func (s *Service) workerNode() worker.Node {
	return &workerNode{
		tnsClient:     s.tnsClient,
		patrickClient: s.patrickClient,
		hoarderClient: s.hoarderClient,
		Node:          s.node,
	}
}

func (w *workerNode) TNS() tnsIface.Client {
	return w.tnsClient
}

func (w *workerNode) Patrick() patrickIface.Client {
	return w.patrickClient
}

func (w *workerNode) Hoarder() hoarderIface.Client {
	return w.hoarderClient
}
