package drive

import (
	"context"
	"io"
	"os"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/mycelium/command"
	"github.com/taubyte/tau/pkg/mycelium/host"
)

// //go:generate mockery --name=remoteHost --output=mocks --outpkg=mocks  --filename=remote.go

// helpers wrapper
type remoteHost interface {
	Sudo(ctx context.Context, name string, args ...string) ([]byte, error)
	Execute(ctx context.Context, name string, args ...string) ([]byte, error)
	Open(path string) (io.ReadCloser, error)
	OpenFile(path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error)
	Remove(name string) error
	RemoveAll(path string) error
	Host() host.Host
}

type remote struct {
	h  host.Host
	fs afero.Fs
}

func newRemote(ctx context.Context, h host.Host) (remoteHost, error) {
	fs, err := h.Fs(ctx)
	if err != nil {
		return nil, err
	}

	return &remote{h: h, fs: fs}, nil
}

func (r *remote) Sudo(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd, err := r.h.Command(ctx, "sudo", command.Args(append([]string{name}, args...)...))
	if err != nil {
		return nil, err
	}
	return cmd.CombinedOutput()
}

func (r *remote) Execute(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd, err := r.h.Command(ctx, name, command.Args(args...))
	if err != nil {
		return nil, err
	}
	return cmd.CombinedOutput()
}

func (r *remote) Open(path string) (io.ReadCloser, error) {
	return r.fs.Open(path)
}

func (r *remote) OpenFile(path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	return r.fs.OpenFile(path, flag, perm)
}

func (r *remote) Remove(name string) error {
	return r.fs.Remove(name)
}

func (r *remote) RemoveAll(path string) error {
	return r.fs.RemoveAll(path)
}

func (r *remote) Host() host.Host {
	return r.h
}
