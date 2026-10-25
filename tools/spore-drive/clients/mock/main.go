package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sshSrv "github.com/taubyte/tau/tools/spore-drive/clients/mock/ssh"
	driveSrv "github.com/taubyte/tau/tools/spore-drive/drive/service"
	healthSrv "github.com/taubyte/tau/tools/spore-drive/health"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	sporedrive "github.com/taubyte/tau/tools/spore-drive"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	dsvr, err := driveSrv.Serve(ctx)
	if err != nil {
		panic(err)
	}

	ssrv, err := sshSrv.Serve(ctx)
	if err != nil {
		panic(err)
	}

	hsvr, err := healthSrv.Serve(ctx, sporedrive.Version)
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	dsvr.Attach(mux)
	ssrv.Attach(mux)
	hsvr.Attach(mux)
	httpServer := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	stopping, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The mock lives as long as its runner holds its stdin open, so a runner
	// killed outright still takes it along. That stdin is a pipe (Python) or a
	// socket (Node); a terminal is no runner, and reading one from the
	// background would stop the mock.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) != 0 {
		go func() {
			io.Copy(io.Discard, os.Stdin)
			stop()
		}()
	}

	go func() {
		<-stopping.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutdownCancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	fmt.Printf("http://localhost:%d/\n", port)

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
