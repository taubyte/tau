package httptun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	keypair "github.com/taubyte/tau/p2p/keypair"

	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	peerService "github.com/taubyte/tau/p2p/streams/service"

	"github.com/taubyte/tau/p2p/streams"
	cr "github.com/taubyte/tau/p2p/streams/command/response"

	logging "github.com/ipfs/go-log/v2"
	peercore "github.com/libp2p/go-libp2p/core/peer"
)

func TestSingleBackend(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx, ctxC := context.WithCancel(context.Background())
	defer ctxC()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))

	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New( // provider
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("Peer creation returned error `%s`", err.Error())
		return
	}
	defer p1.Close()

	svr, err := peerService.New(p1, "gw", "/gw/1.0")
	if err != nil {
		t.Errorf("Service creation returned error `%s`", err.Error())
		return
	}
	defer svr.Stop()
	err = svr.DefineStream(
		"tun",
		func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
			return cr.Response{"up": true}, nil
		},
		func(ctx context.Context, rw io.ReadWriter) {
			w, r, err := Backend(rw)
			if err != nil {
				t.Error(err)
				return
			}

			w.Header().Set("X-XSS-Protection", "0")
			w.WriteHeader(200)

			buf := make([]byte, 4*1024)
			defer r.Body.Close()
			for {
				n, err := r.Body.Read(buf)
				if n > 0 {
					upper := strings.ToUpper(string(buf[:n]))
					_, err = w.Write([]byte(upper))
				}
				if err != nil {
					break
				}
			}
		},
	)
	if err != nil {
		t.Error(err)
		return
	}

	p2, err := peer.New( // consumer
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n+1)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("Ping test returned error `%s`", err.Error())
		return
	}
	defer p2.Close()

	err = p2.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()})
	if err != nil {
		t.Errorf("Connect to peer %v returned `%s`", p1.Peer().Addrs(), err.Error())
		return
	}

	c, err := client.New(p2, "/gw/1.0")
	if err != nil {
		t.Error(err)
		return
	}

	go http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", n+10), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		respCh, err := c.New("tun", client.To(p1.ID())).Do()
		if err != nil {
			t.Errorf("Command returned error `%s`", err.Error())
			return
		}

		res := <-respCh
		if res == nil {
			t.Error("Command timed out")
			return
		}
		defer res.Close()

		if err := res.Error(); err != nil {
			t.Errorf("error %s", err.Error())
			return
		}

		if v, k := res.Get("up"); k != nil || !v.(bool) {
			t.Error("provider can not handle request")
			return
		}

		err = Frontend(w, r, res)
		if err != nil {
			t.Error(err)
		}
	}))

	time.Sleep(3 * time.Second)

	msg := "a%08db"
	var str string
	multiple := 1024 * 1024 / len(msg)
	for i := 0; i < multiple; i++ {
		str += fmt.Sprintf(msg, i)
	}

	upper := strings.ToUpper(str)
	buf := bytes.NewBuffer([]byte(str))

	req, err := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d", n+10), buf)
	if err != nil {
		t.Error(err)
		return
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != 200 {
		t.Error("response code", res.StatusCode)
	}

	resBuf, err := io.ReadAll(res.Body)
	if err != nil {
		t.Error(err)
		return
	}

	resStr := string(resBuf)
	for i := 0; i < len(resStr); i++ {
		if resStr[i] != upper[i] {
			t.Error("response does not match", i)
			break
		}

	}
}
