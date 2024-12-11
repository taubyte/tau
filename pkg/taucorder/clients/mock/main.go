package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/taubyte/tau/dream"
	dreamApi "github.com/taubyte/tau/dream/api"
	srv "github.com/taubyte/tau/pkg/taucorder/service"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	dreamClient "github.com/taubyte/tau/clients/http/dream"

	_ "github.com/taubyte/tau/utils/dream"
)

func main() {
	dream.DreamlandApiListen = "localhost:2442" // diffrent port than the default

	dreamApi.BigBang()

	uname := "mock_universe"

	// Create a new universe
	u := dream.New(dream.UniverseConfig{
		Name: uname,
	})
	defer u.Stop()

	// Start the universe with basic config
	err := u.StartAll()
	if err != nil {
		panic(err)
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port

	// Create taucorder service
	tcsvr, err := srv.Serve(context.Background(), nil)
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	tcsvr.Attach(mux)

	httpServer := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-signalChan
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
		defer shutdownCancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	dreamClient, err := dreamClient.New(ctx, dreamClient.URL("http://127.0.0.1:2442"))
	if err != nil {
		panic(err)
	}

	status, err := dreamClient.Universe(uname).Chart()
	if err != nil {
		panic(err)
	}

	data := struct {
		Url   string                 `json:"url"`
		Nodes []*dreamApi.EchartNode `json:"nodes"`
	}{
		Url:   fmt.Sprintf("http://localhost:%d/", port),
		Nodes: status.Nodes,
	}

	printOut, err := json.Marshal(data)
	if err != nil {
		panic(err)

	}
	fmt.Printf("@@%s@@\n", printOut)

	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		panic(err)
	}
}
