// Package vault serves the secrets a build or a container was granted to what runs
// inside it, over a unix socket mounted beside a static helper that reads one:
//
//	/.taubyte/secret <label>
//
// A secret never enters the environment. It is read when the process asks, by the
// label the author wrote, and a value served is masked in the log from then on (see
// Redactor). The helper is written in Zig (helper/secret.zig, `make vault-helper`) so
// an image needs no curl, no shell and no libc to use it.
package vault

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

const (
	// Dir is where the vault appears inside the container.
	Dir = "/.taubyte"

	// Socket and Helper are the vault's and the helper's names in Dir. The helper
	// dials Dir/Socket, a path compiled into it.
	Socket = "vault.sock"
	Helper = "secret"
)

// ErrNotGranted is a label the process was not granted: a refusal, told to the
// process as one.
var ErrNotGranted = errors.New("vault: not granted")

// Reader answers a secret by its label.
type Reader interface {
	Read(ctx context.Context, label string) ([]byte, error)
}

// Server is one vault on this machine, for one build or one container.
type Server struct {
	root   string
	server *http.Server
	done   chan error
}

// Serve starts a vault answering from r. served is told every value before the
// process receives it, so a log can mask it before the process can print it.
//
// The helper is written for amd64 and arm64. On any other machine the socket is
// served without it.
func Serve(r Reader, served func([]byte)) (*Server, error) {
	// The root is private to this user; only the directory inside it is mounted,
	// so nothing on the host but this process reaches the socket.
	root, err := os.MkdirTemp("", "tau-vault-")
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	s := &Server{root: root, done: make(chan error, 1)}
	if err := s.start(r, served); err != nil {
		os.RemoveAll(root)
		return nil, err
	}
	return s, nil
}

func (s *Server) start(r Reader, served func([]byte)) error {
	dir := s.Dir()
	if err := os.Mkdir(dir, 0o755); err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	if helper := helperFor(runtime.GOARCH); helper != nil {
		if err := os.WriteFile(filepath.Join(dir, Helper), helper, 0o755); err != nil {
			return fmt.Errorf("vault: %w", err)
		}
	}

	listener, err := net.Listen("unix", filepath.Join(dir, Socket))
	if err != nil {
		return fmt.Errorf("vault: %w", err)
	}
	// What runs in the container may not be root.
	if err := os.Chmod(filepath.Join(dir, Socket), 0o666); err != nil {
		listener.Close()
		return fmt.Errorf("vault: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/secret/{label}", func(w http.ResponseWriter, req *http.Request) {
		label := req.PathValue("label")
		value, err := r.Read(req.Context(), label)
		switch {
		case errors.Is(err, ErrNotGranted):
			// The reader's reason, which says why: never declared, refused, or a verb
			// not asked for.
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		case err != nil:
			http.Error(w, "reading "+label+": "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if served != nil {
			served(value)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(value)
	})

	s.server = &http.Server{Handler: mux}
	go func() { s.done <- s.server.Serve(listener) }()
	return nil
}

// Dir is the directory on this machine to mount at Dir.
func (s *Server) Dir() string { return filepath.Join(s.root, "mnt") }

// Close stops the vault and removes it from this machine.
func (s *Server) Close() error {
	err := s.server.Close()
	if served := <-s.done; !errors.Is(served, http.ErrServerClosed) {
		err = errors.Join(err, served)
	}
	return errors.Join(err, os.RemoveAll(s.root))
}
