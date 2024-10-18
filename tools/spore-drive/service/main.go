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
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
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

	dsvr, err := driveSrv.Serve(context.Background(), csvr)
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	csvr.Attach(mux)
	dsvr.Attach(mux)

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
