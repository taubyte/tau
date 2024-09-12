package runtime

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	"archive/tar"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/spf13/afero"
	"github.com/spf13/afero/tarfs"
	"github.com/spf13/afero/zipfs"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental/sock"
	"github.com/tetratelabs/wazero/sys"

	crand "crypto/rand"

	"github.com/moby/moby/pkg/namesgenerator"

	gvnvirtualnetwork "github.com/containers/gvisor-tap-vsock/pkg/virtualnetwork"

	//lint:ignore ST1001 ignore
	. "github.com/taubyte/tau/pkg/spin"
)

type mountPoint string
type mountSource string

type container struct {
	ctx  context.Context
	ctxC context.CancelFunc

	parent *spin

	name string
	cmd  []string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	closers chan io.Closer

	done chan struct{}

	mounts map[mountPoint]mountSource

	networking *NetworkConfig
	vn         *gvnvirtualnetwork.VirtualNetwork
	port       int
	sockCfg    sock.Config

	bundle string

	env map[string]string

	module api.Module
}

func Name(name string) Option[Container] {
	return func(c Container) error {
		c.(*container).name = name
		return nil
	}
}

func Command(cmd ...string) Option[Container] {
	return func(c Container) error {
		c.(*container).cmd = cmd
		return nil
	}
}

func Mount(hostDir, wasmDir string) Option[Container] {
	return func(c Container) error {
		if !path.IsAbs(hostDir) {
			return errors.New("mount host directory must be absolute")
		}
		if !path.IsAbs(wasmDir) {
			return errors.New("mount directory must be absolute")
		}
		c.(*container).mounts[mountPoint(wasmDir)] = mountSource(hostDir)
		return nil
	}
}

func ImageFile(path string) Option[Container] {
	return func(ci Container) error {
		c := ci.(*container)
		if !c.parent.isRuntime {
			return errors.New("only runtimes can use bundles")
		}
		c.bundle = path
		return nil
	}
}

func Image(name string) Option[Container] {
	return func(ci Container) error {
		c := ci.(*container)
		if !c.parent.isRuntime {
			return errors.New("only runtimes can use bundles")
		}
		if c.parent.registry == nil {
			return errors.New("no registry")
		}
		imagePath, err := c.parent.registry.Path(name)
		if err != nil {
			return errors.New("need to pull first")
		}
		c.bundle = imagePath

		return nil
	}
}

func Stdin(r io.Reader) Option[Container] {
	return func(ci Container) error {
		ci.(*container).stdin = r
		return nil
	}
}

func Stdout(w io.Writer) Option[Container] {
	return func(ci Container) error {
		ci.(*container).stdout = w
		return nil
	}
}

func Stderr(w io.Writer) Option[Container] {
	return func(ci Container) error {
		ci.(*container).stderr = w
		return nil
	}
}

func Env(k, v string) Option[Container] {
	return func(ci Container) error {
		ci.(*container).env[k] = v
		return nil
	}
}

func Subnet(n string) Option[*NetworkConfig] {
	return func(nc *NetworkConfig) error {
		ip, ipNet, err := net.ParseCIDR("192.168.1.0/24")
		if err != nil {
			return err
		}
		if ip.To4() == nil {
			return errors.New("container network is not IPv4")
		}
		nc.network = ipNet
		return nil
	}
}

func GuestAddress(i string) Option[*NetworkConfig] {
	return func(nc *NetworkConfig) error {
		parsedIP := net.ParseIP(i)
		if parsedIP == nil || parsedIP.To4() == nil {
			return errors.New("container address is not IPv4")
		}
		nc.ip = parsedIP
		return nil
	}
}

func GuestMAC(mac string) Option[*NetworkConfig] {
	return func(nc *NetworkConfig) (err error) {
		nc.mac, err = net.ParseMAC(mac)
		return
	}
}

func Forward(host, guest string) Option[*NetworkConfig] {
	return func(nc *NetworkConfig) error {
		var (
			hIP   = "0.0.0.0"
			hPort string
		)
		_, err := strconv.Atoi(host)
		if err != nil {
			hIP, hPort, err = net.SplitHostPort(host)
			if err != nil {
				return err
			}
		} else {
			hPort = host
		}

		if _, err = strconv.Atoi(guest); err != nil {
			return errors.New("guest must be a valid port number")
		}

		nc.forwards[hIP+":"+hPort] = guest // will need a second pass

		return nil
	}
}

func Networking(options ...Option[*NetworkConfig]) Option[Container] {
	return func(ci Container) error {
		nc := &NetworkConfig{
			network:  DefaultNetwork,
			ip:       DefaultIPAddress,
			mac:      DefaultContainerMacAddress,
			forwards: make(map[string]string),
		}

		for _, opt := range options {
			if err := opt(nc); err != nil {
				return nil
			}
		}

		for k, v := range nc.forwards {
			nc.forwards[k] = nc.ip.String() + ":" + v
		}

		ci.(*container).networking = nc

		return nil
	}
}

func (s *spin) New(options ...Option[Container]) (Container, error) {
	c := &container{
		parent:  s,
		mounts:  make(map[mountPoint]mountSource),
		closers: make(chan io.Closer, 128),
		stdin:   nil,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		done:    make(chan struct{}, 1),
		env:     make(map[string]string),
	}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if err := s.init(c); err != nil {
		return nil, err
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	s.containers[c.name] = c

	return c, nil
}

func (s *spin) init(c *container) (err error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	if c.name == "" {
		c.name = namesgenerator.GetRandomName(0)
		// try one more time
		if _, exists := s.containers[c.name]; exists {
			c.name = namesgenerator.GetRandomName(1)
		}
	}

	if _, exists := s.containers[c.name]; exists {
		return fmt.Errorf("container `%s` alreay exists", c.name)
	}

	c.ctx, c.ctxC = context.WithCancel(s.ctx)

	fsConfig := wazero.NewFSConfig()
	for wasmDir, hostDir := range c.mounts {
		st, err := os.Stat(string(hostDir))
		if err != nil {
			return fmt.Errorf("looking up %s failed with %w", hostDir, err)
		}

		if st.IsDir() {
			fsConfig = fsConfig.WithDirMount(string(hostDir), string(wasmDir))
		} else {
			t, err := filetype.MatchFile(string(hostDir))
			if err != nil {
				return err
			}

			switch t {
			case matchers.TypeZip:
				zipReader, err := zip.OpenReader(string(hostDir))
				if err != nil {
					return err
				}
				c.closers <- zipReader

				fsConfig = fsConfig.WithFSMount(afero.NewIOFS(zipfs.New(&zipReader.Reader)), string(wasmDir))
			case matchers.TypeTar:
				tf, err := os.Open(string(hostDir))
				if err != nil {
					return err
				}
				c.closers <- tf

				fsConfig = fsConfig.WithFSMount(afero.NewIOFS(tarfs.New(tar.NewReader(tf))), string(wasmDir))
			default:
				return errors.New("unsupported file format for mount")
			}
		}
	}

	if s.isRuntime && c.bundle != "" {
		zipReader, err := zip.OpenReader(c.bundle)
		if err != nil {
			return err
		}
		c.closers <- zipReader

		fsConfig = fsConfig.WithFSMount(afero.NewIOFS(zipfs.New(&zipReader.Reader)), "/ext/bundle")
	}

	cmd := []string{"vm"}
	if c.stdin == nil {
		cmd = append(cmd, "-no-stdin")
	}
	if c.networking != nil {
		cmd = append(cmd, "-net=socket")
	}
	cmd = append(cmd, c.cmd...)

	config := wazero.
		NewModuleConfig().
		WithStdin(c.stdin).
		WithStdout(c.stdout).
		WithStderr(c.stderr).
		WithName(c.name).
		WithFSConfig(fsConfig).
		WithArgs(cmd...).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(crand.Reader).
		WithStartFunctions() // don't start yet

	// Environment
	for k, v := range c.env {
		config = config.WithEnv(k, v)
	}

	if c.networking != nil {
		if err = c.initNetwork(c.ctx); err != nil {
			return err
		}
		c.ctx = sock.WithConfig(c.ctx, c.sockCfg)
	}

	if c.module, err = s.runtime.InstantiateModule(c.ctx, s.module, config); err != nil {
		return fmt.Errorf("instantiate module %s failed with %w", c.name, err)
	}

	if c.networking != nil {
		var conn net.Conn
		addr := fmt.Sprintf("127.0.0.1:%d", c.port)
	tryConnect:
		for i := 0; i < 100; i++ {
			select {
			case <-c.ctx.Done():
				return c.ctx.Err()
			case <-time.After(100 * time.Millisecond):
				conn, err = net.Dial("tcp", addr)
				if err == nil {
					break tryConnect
				}
			}
		}

		if conn == nil {
			return errors.New("failed to establish connection")
		}

		// We register our VM network as a qemu "-netdev socket".
		go func() {
			if err := c.vn.AcceptQemu(c.ctx, conn); err != nil {
				c.ctxC()
				fmt.Fprintf(os.Stderr, "failed AcceptQemu: %v\n", err)
			}
		}()
	}

	return nil
}

func (c *container) Run() error {
	defer func() {
		c.done <- struct{}{}
	}()

	main := c.module.ExportedFunction("_start")
	if main == nil {
		return errors.New("no main")
	}

	if _, err := main.Call(c.ctx); err != nil {
		if err.Error() == "module closed with context canceled" {
			return nil
		} else if se, ok := err.(*sys.ExitError); ok {
			if se.ExitCode() == 0 { // Don't err on success.
				err = nil
			}
			return err
		} else {
			return fmt.Errorf("executing %s failed with %w", main.Definition().Name(), err)
		}
	}

	return nil
}

func (s *spin) remove(c *container) {
	s.lock.Lock()
	defer s.lock.Unlock()

	delete(s.containers, c.name)
}

func (c *container) Stop() {
	c.ctxC()
	<-c.done // wait for it to stop

	close(c.closers)
	for closer := range c.closers {
		closer.Close()
	}

	c.parent.remove(c)
}
