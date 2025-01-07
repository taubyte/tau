package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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

	runData, err := NewRunFile()
	if err != nil {
		panic(err)
	}
	defer runData.Remove()

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	err = runData.Save(listener)
	if err != nil {
		panic(err)
	}

	csvr, err := confSrv.Serve()
	if err != nil {
		panic(err)
	}

	dsvr, err := driveSrv.Serve(ctx, csvr)
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
	hsvr.Attach(mux)

	server := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	shutdownChan := make(chan struct{})

	go func() {
		<-signalChan
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		close(shutdownChan)
	}()

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}

	<-shutdownChan
}
