package main

import (
	"context"
	"fmt"
	"net"
	"net/http"

	sshSrv "github.com/taubyte/tau/pkg/spore-drive/clients/mock/ssh"
	confSrv "github.com/taubyte/tau/pkg/spore-drive/config/service"
	driveSrv "github.com/taubyte/tau/pkg/spore-drive/drive/service"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func main() {
	// Create a new listener on a dynamic port (":0" means any available port)
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}

	// Get the dynamically assigned port
	port := listener.Addr().(*net.TCPAddr).Port

	csvr, err := confSrv.Serve()
	if err != nil {
		panic(err)
	}

	dsvr, err := driveSrv.Serve(context.Background(), csvr)
	if err != nil {
		panic(err)
	}

	ssrv, err := sshSrv.Serve(context.Background())
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	csvr.Attach(mux)
	dsvr.Attach(mux)
	ssrv.Attach(mux)

	defer listener.Close()

	fmt.Printf("http://localhost:%d/\n", port)

	err = http.Serve(
		listener,
		// Use h2c so we can serve HTTP/2 without TLS.
		h2c.NewHandler(mux, &http2.Server{}),
	)

	if err != nil {
		panic(err)
	}
}
