package raft

import (
	"context"
	"crypto/cipher"
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

type namespaceStreamLayer struct {
	host             host.Host
	protocol         protocol.ID
	listener         net.Listener
	encryptionCipher cipher.AEAD
}

func newNamespaceStreamLayer(h host.Host, namespace string, encryptionCipher cipher.AEAD) (*namespaceStreamLayer, error) {
	protocolID := protocol.ID(TransportProtocol(namespace))

	listener, err := gostream.Listen(h, protocolID)
	if err != nil {
		return nil, err
	}

	return &namespaceStreamLayer{
		host:             h,
		protocol:         protocolID,
		listener:         listener,
		encryptionCipher: encryptionCipher,
	}, nil
}

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
	conn, err := gostream.Dial(ctx, sl.host, pid, sl.protocol)
	if err != nil {
		return nil, err
	}

	if sl.encryptionCipher != nil {
		return newEncryptedConn(conn, sl.encryptionCipher)
	}

	return conn, nil
}

func (sl *namespaceStreamLayer) Accept() (net.Conn, error) {
	conn, err := sl.listener.Accept()
	if err != nil {
		return nil, err
	}

	if sl.encryptionCipher != nil {
		return newEncryptedConn(conn, sl.encryptionCipher)
	}

	return conn, nil
}

func (sl *namespaceStreamLayer) Addr() net.Addr {
	return sl.listener.Addr()
}

func (sl *namespaceStreamLayer) Close() error {
	return sl.listener.Close()
}

type namespaceAddrProvider struct {
}

func (ap *namespaceAddrProvider) ServerAddr(id raft.ServerID) (raft.ServerAddress, error) {
	return raft.ServerAddress(id), nil
}

func newNamespaceTransport(h host.Host, namespace string, timeout time.Duration, encryptionCipher cipher.AEAD) (*raft.NetworkTransport, error) {
	provider := &namespaceAddrProvider{}
	stream, err := newNamespaceStreamLayer(h, namespace, encryptionCipher)
	if err != nil {
		return nil, err
	}

	cfg := &raft.NetworkTransportConfig{
		ServerAddressProvider: provider,
		Logger:                &hcLogToLogger{},
		Stream:                stream,
		MaxPool:               0,
		Timeout:               timeout,
	}

	return raft.NewNetworkTransportWithConfig(cfg), nil
}
