package host

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/taubyte/tau/pkg/mycelium/auth"
	"github.com/taubyte/tau/pkg/mycelium/command"
	"golang.org/x/crypto/ssh"

	"github.com/spf13/afero"
	"github.com/spf13/afero/sftpfs"
)

type Host interface {
	Clone(attributes ...Attribute) (Host, error)
	Command(ctx context.Context, name string, options ...command.Option) (*command.Command, error)
	Fs(ctx context.Context, opts ...sftp.ClientOption) (afero.Fs, error)
	Tags() []string
	String() string
	Name() string
}

type host struct {
	lock    sync.Mutex
	client  *ssh.Client
	name    string
	addr    string
	port    uint64
	timeout time.Duration
	auth    []*auth.Auth
	key     ssh.PublicKey
	tags    []string
}

func New(attributes ...Attribute) (Host, error) {
	h := &host{
		port: 22,
	}
	for _, attr := range attributes {
		if err := attr(h); err != nil {
			return nil, fmt.Errorf("applying attribute: %w", err)
		}
	}

	return h, nil
}

func (h *host) Clone(attributes ...Attribute) (Host, error) {
	hc := &host{
		name:    h.name,
		addr:    h.addr,
		port:    h.port,
		timeout: h.timeout,
	}

	if h.key != nil {
		hc.key, _ = ssh.ParsePublicKey(h.key.Marshal())
	}

	hc.auth = append(hc.auth, h.auth...)

	hc.tags = append(hc.tags, h.tags...)

	for _, attr := range attributes {
		if err := attr(hc); err != nil {
			return nil, fmt.Errorf("applying attribute: %w", err)
		}
	}

	return hc, nil
}

func (h *host) Command(ctx context.Context, name string, options ...command.Option) (*command.Command, error) {
	sess, err := h.newSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating new session: %w", err)
	}

	return command.New(ctx, sess, name, options...)
}

func (h *host) forceClose() {
	h.client.Close()
	h.client = nil
}

func (h *host) newSession(ctx context.Context) (*ssh.Session, error) {
	cCtx, cCtxC := context.WithTimeout(ctx, 10*time.Second)
	defer cCtxC()
	h.lock.Lock()
	defer h.lock.Unlock()
	for {
		select {
		case <-cCtx.Done():
			return nil, cCtx.Err()
		default:
			if err := h.tryInit(); err != nil {
				return nil, fmt.Errorf("initializing host: %w", err)
			}

			sess, err := h.client.NewSession()
			if err == nil {
				return sess, nil
			}

			// we got an error -> probably connection was closed.
			// clean up before retry
			h.forceClose()

			// cool down
			select {
			case <-cCtx.Done():
				return nil, cCtx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

func (h *host) String() string {
	return net.JoinHostPort(h.addr, strconv.FormatUint(h.port, 10))
}

func (h *host) Name() string {
	return h.name
}

func (h *host) Tags() []string {
	return h.tags
}

func (h *host) tryInit() (err error) {
	if h.client == nil {
		var latestErr error
		for _, am := range h.auth {
			keyCallback := ssh.InsecureIgnoreHostKey()
			if h.key != nil {
				keyCallback = ssh.FixedHostKey(h.key)
			}

			var err error
			h.client, err = ssh.Dial(
				"tcp",
				net.JoinHostPort(h.addr, strconv.FormatUint(h.port, 10)),
				&ssh.ClientConfig{
					User:            am.Username,
					Auth:            am.Auth,
					Timeout:         h.timeout,
					HostKeyCallback: keyCallback,
				},
			)
			if err == nil {
				break
			} else {
				latestErr = err
			}
		}
		if h.client == nil {
			return fmt.Errorf("failed to initialize SSH client: %w", latestErr)
		}
	}

	return nil
}

func (h *host) Fs(ctx context.Context, opts ...sftp.ClientOption) (afero.Fs, error) {
	cCtx, cCtxC := context.WithTimeout(ctx, 10*time.Second)
	defer cCtxC()
	h.lock.Lock()
	defer h.lock.Unlock()
	for {
		select {
		case <-cCtx.Done():
			return nil, cCtx.Err()
		default:
			if err := h.tryInit(); err != nil {
				return nil, fmt.Errorf("initializing host: %w", err)
			}

			sclient, err := sftp.NewClient(h.client, opts...)
			if err == nil {
				return sftpfs.New(sclient), nil
			}

			// we got an error -> probably connection was closed.
			// clean up before retry
			h.forceClose()

			// cool down
			select {
			case <-cCtx.Done():
				return nil, cCtx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}
