package service

import (
	"context"
	"os"

	iface "github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
	tnsClient "github.com/taubyte/go-interfaces/services/tns"
	patrickClient "github.com/taubyte/odo/clients/p2p/patrick"
	"github.com/taubyte/odo/config"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"
)

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
}

type Service struct {
	ctx           context.Context
	node          peer.Node
	stream        *streams.CommandService
	monkeys       map[string]*Monkey
	patrickClient patrick.Client
	tnsClient     tnsClient.Client
	odoClientNode peer.Node

	dev         bool
	dvPublicKey []byte
}

func (s *Service) Patrick() patrick.Client {
	return s.patrickClient
}

func (s *Service) Delete(jid string) {
	delete(s.monkeys, jid)
}

func (s *Service) Node() peer.Node {
	return s.node
}

func (s *Service) Dev() bool {
	return s.dev
}

type Config config.Protocol
