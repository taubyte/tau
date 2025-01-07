package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sshSrv "github.com/taubyte/tau/pkg/spore-drive/clients/mock/ssh"
	confSrv "github.com/taubyte/tau/pkg/spore-drive/config/service"
	driveSrv "github.com/taubyte/tau/pkg/spore-drive/drive/service"
	healthSrv "github.com/taubyte/tau/pkg/spore-drive/health"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	sporedrive "github.com/taubyte/tau/pkg/spore-drive"
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

	csvr, err := confSrv.Serve()
	if err != nil {
		panic(err)
	}

	dsvr, err := driveSrv.Serve(ctx, csvr)
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
	csvr.Attach(mux)
	dsvr.Attach(mux)
	ssrv.Attach(mux)
	hsvr.Attach(mux)
	httpServer := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-signalChan
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutdownCancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	fmt.Printf("http://localhost:%d/\n", port)

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
