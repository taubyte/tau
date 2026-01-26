package raft

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	logging "github.com/ipfs/go-log/v2"
	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var raftTransportLogger = logging.Logger("raft-transport")

// HcLogToLogger implements github.com/hashicorp/go-hclog for Raft transport
type hcLogToLogger struct {
	extraArgs []interface{}
	name      string
}

func (log *hcLogToLogger) formatArgs(args []interface{}) string {
	result := ""
	args = append(args, log.extraArgs)
	for i := 0; i < len(args); i = i + 2 {
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		val := args[i+1]
		result += fmt.Sprintf("%s=%v. ", key, val)
	}
	return result
}

func (log *hcLogToLogger) format(msg string, args []interface{}) string {
	argstr := log.formatArgs(args)
	if len(argstr) > 0 {
		argstr = ". Args: " + argstr
	}
	name := log.name
	if len(name) > 0 {
		name += ": "
	}
	return name + msg + argstr
}

func (log *hcLogToLogger) Log(level hclog.Level, msg string, args ...interface{}) {
	switch level {
	case hclog.Trace, hclog.Debug:
		log.Debug(msg, args...)
	case hclog.NoLevel, hclog.Info:
		log.Info(msg, args...)
	case hclog.Warn:
		log.Warn(msg, args...)
	case hclog.Error:
		log.Error(msg, args...)
	default:
		log.Warn(msg, args...)
	}
}

func (log *hcLogToLogger) Trace(msg string, args ...interface{}) {
	raftTransportLogger.Debug(log.format(msg, args))
}

func (log *hcLogToLogger) Debug(msg string, args ...interface{}) {
	raftTransportLogger.Debug(log.format(msg, args))
}

func (log *hcLogToLogger) Info(msg string, args ...interface{}) {
	raftTransportLogger.Info(log.format(msg, args))
}

func (log *hcLogToLogger) Warn(msg string, args ...interface{}) {
	raftTransportLogger.Warn(log.format(msg, args))
}

func (log *hcLogToLogger) Error(msg string, args ...interface{}) {
	raftTransportLogger.Error(log.format(msg, args))
}

func (log *hcLogToLogger) IsTrace() bool { return true }
func (log *hcLogToLogger) IsDebug() bool { return true }
func (log *hcLogToLogger) IsInfo() bool  { return true }
func (log *hcLogToLogger) IsWarn() bool  { return true }
func (log *hcLogToLogger) IsError() bool { return true }

func (log *hcLogToLogger) Name() string {
	return log.name
}

func (log *hcLogToLogger) With(args ...interface{}) hclog.Logger {
	return &hcLogToLogger{extraArgs: args, name: log.name}
}

func (log *hcLogToLogger) Named(name string) hclog.Logger {
	return &hcLogToLogger{name: log.name + ": " + name}
}

func (log *hcLogToLogger) ResetNamed(name string) hclog.Logger {
	return &hcLogToLogger{name: name}
}

func (log *hcLogToLogger) SetLevel(level hclog.Level) {}

func (log *hcLogToLogger) GetLevel() hclog.Level {
	return hclog.LevelFromString("DEBUG")
}

func (log *hcLogToLogger) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	return nil
}

func (log *hcLogToLogger) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	return nil
}

func (log *hcLogToLogger) ImpliedArgs() []interface{} {
	return nil
}

// namespaceStreamLayer implements raft.StreamLayer using namespace-specific protocols
// This allows multiple clusters on the same node without interference
type namespaceStreamLayer struct {
	host     host.Host
	protocol protocol.ID
	listener net.Listener
}

// newNamespaceStreamLayer creates a new stream layer with namespace-specific protocol
func newNamespaceStreamLayer(h host.Host, namespace string) (*namespaceStreamLayer, error) {
	// Use namespace-specific protocol for Raft RPC
	// This ensures different clusters don't interfere
	protocolID := protocol.ID(TransportProtocol(namespace))

	listener, err := gostream.Listen(h, protocolID)
	if err != nil {
		return nil, err
	}

	return &namespaceStreamLayer{
		host:     h,
		protocol: protocolID,
		listener: listener,
	}, nil
}

// Dial opens a connection to the target peer using namespace-specific protocol
func (sl *namespaceStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	if sl.host == nil {
		return nil, errors.New("streamLayer not initialized")
	}

	pid, err := peer.Decode(string(address))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return gostream.Dial(ctx, sl.host, pid, sl.protocol)
}

// Accept accepts incoming connections
func (sl *namespaceStreamLayer) Accept() (net.Conn, error) {
	return sl.listener.Accept()
}

// Addr returns the listener's address
func (sl *namespaceStreamLayer) Addr() net.Addr {
	return sl.listener.Addr()
}

// Close closes the listener
func (sl *namespaceStreamLayer) Close() error {
	return sl.listener.Close()
}

// namespaceAddrProvider provides server addresses
type namespaceAddrProvider struct {
	h host.Host
}

// ServerAddr takes a raft.ServerID and returns it as a ServerAddress
func (ap *namespaceAddrProvider) ServerAddr(id raft.ServerID) (raft.ServerAddress, error) {
	return raft.ServerAddress(id), nil
}

// newNamespaceTransport creates a namespace-aware Raft transport
func newNamespaceTransport(h host.Host, namespace string, timeout time.Duration) (*raft.NetworkTransport, error) {
	provider := &namespaceAddrProvider{h}
	stream, err := newNamespaceStreamLayer(h, namespace)
	if err != nil {
		return nil, err
	}

	// Configuration for raft.NetworkTransport initialized with our namespace-specific StreamLayer
	// MaxPool is set to 0 so the NetworkTransport does not pool connections.
	// We are multiplexing streams over an already created Libp2p connection, which is cheap.
	cfg := &raft.NetworkTransportConfig{
		ServerAddressProvider: provider,
		Logger:                &hcLogToLogger{},
		Stream:                stream,
		MaxPool:               0,
		Timeout:               timeout,
	}

	return raft.NewNetworkTransportWithConfig(cfg), nil
}
