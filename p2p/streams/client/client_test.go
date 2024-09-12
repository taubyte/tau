package client

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"

	keypair "github.com/taubyte/tau/p2p/keypair"

	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams/command"
	peerService "github.com/taubyte/tau/p2p/streams/service"

	"github.com/taubyte/tau/p2p/streams"
	cr "github.com/taubyte/tau/p2p/streams/command/response"

	logging "github.com/ipfs/go-log/v2"
	peercore "github.com/libp2p/go-libp2p/core/peer"
)

func TestClientSend(t *testing.T) {
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

	svr, err := peerService.New(p1, "hello", "/hello/1.0")
	if err != nil {
		t.Errorf("Service creation returned error `%s`", err.Error())
		return
	}
	defer svr.Stop()
	err = svr.Define("hi", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"message": "HI"}, nil
	})
	if err != nil {
		t.Error(err)
		return
	}

	err = svr.Define("echo", func(_ctx context.Context, _ streams.Connection, _body command.Body) (cr.Response, error) {
		return cr.Response{"message": _body["message"].(string)}, nil
	})
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

	// static peers []string{p1.ID().String()}
	c, err := New(p2, "/hello/1.0")
	if err != nil {
		t.Errorf("Client creation returned error `%s`", err.Error())
		return
	} else {
		// no arg command
		resCh, err := c.New("hi", To(p1.ID())).Do()
		if err != nil {
			t.Errorf("Sending command returned error `%s`", err.Error())
			return
		} else {
			res := <-resCh
			if res == nil {
				t.Error("Command timed out")
				return
			}
			defer res.Close()
			if err := res.Error(); err != nil {
				t.Errorf("error %s", err.Error())
				return
			}
			if v, err := res.Get("message"); err != nil || v.(string) != "HI" {
				t.Errorf("Provider response does not match %v", v)
				return
			}
		}

		// command with argument
		resCh, err = c.New("echo", Body(command.Body{"message": "back"})).Do()
		if err != nil {
			t.Errorf("Sending command returned error `%s`", err.Error())
			return
		} else {
			res := <-resCh
			if res == nil {
				t.Error("Command timed out")
				return
			}
			defer res.Close()
			if err := res.Error(); err != nil {
				t.Errorf("error %s", err.Error())
				return
			}
			if v, err := res.Get("message"); err != nil || v.(string) != "back" {
				t.Errorf("Provider response does not match %v", v)
				return
			}
		}

		// command with big argument
		bigMessageBase := "1234567890qwertyuiopasdfghjklzxcvbnm1234567890qwertyuiopasdfghjklzxcvbnm"
		var bigMessage string
		bigMessageCount := 1024 * 1024 / len(bigMessageBase)
		for i := 0; i < bigMessageCount; i++ {
			bigMessage += bigMessageBase
		}

		resCh, err = c.New("echo", Body(command.Body{"message": bigMessage})).Do()
		if err != nil {
			t.Errorf("Sending command returned error `%s`", err.Error())
			return
		} else {
			res := <-resCh
			if res == nil {
				t.Error("Command timed out")
				return
			}
			defer res.Close()

			if err := res.Error(); err != nil {
				t.Errorf("error %s", err.Error())
				return
			}
			if v, err := res.Get("message"); err != nil || v.(string) != bigMessage {
				t.Errorf("Provider response does not match %v", v)
				return
			}
		}

		//invalid command
		resCh, err = c.New("notExist").Do()
		if err == nil && len(resCh) != 0 {
			t.Error("Non existing command not handled correctly")
			return
		}

	}
	// Close
	c.Close()

	// discover
	cd, err := New(p2, "/hello/1.0")
	if err != nil {
		t.Errorf("Client creation returned error `%s`", err.Error())
		return
	} else {
		resCh, err := cd.New("hi").Do()
		if err != nil {
			t.Errorf("Sending command returned error `%s`", err.Error())
			return
		} else {
			res := <-resCh
			if res == nil {
				t.Error("Command timed out")
				return
			}
			defer res.Close()
			if err := res.Error(); err != nil {
				t.Errorf("error %s", err.Error())
				return
			}
			if v, err := res.Get("message"); err != nil || v.(string) != "HI" {
				t.Errorf("Provider response does not match %v", v)
				return
			}
		}
	}

	//Close
	cd.Close()
}

func TestClientUpgrade(t *testing.T) {
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

	svr, err := peerService.New(p1, "hello", "/hello/1.0")
	if err != nil {
		t.Errorf("Service creation returned error `%s`", err.Error())
		return
	}
	defer svr.Stop()
	err = svr.DefineStream(
		"hi",
		func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
			return cr.Response{"message": "HI"}, nil
		},
		func(ctx context.Context, rw io.ReadWriter) {
			buf := make([]byte, 1024)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					n, err := rw.Read(buf)
					if n > 0 {
						_, err := rw.Write([]byte(strings.ToUpper(string(buf[:n]))))
						if err != nil {
							return
						}
					}
					if err != nil {
						return
					}

				}
			}
		},
	)
	if err != nil {
		t.Error(err)
		return
	}

	err = svr.DefineStream(
		"hi2",
		func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
			return nil, nil
		},
		func(ctx context.Context, rw io.ReadWriter) {
			buf := make([]byte, 1024)
			n, _ := rw.Read(buf)
			if n > 0 {
				rw.Write([]byte(strings.ToUpper(string(buf[:n]))))
			}
		},
	)
	if err != nil {
		t.Error(err)
		return
	}

	err = svr.DefineStream(
		"hi3",
		func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
			return nil, nil
		},
		func(ctx context.Context, rw io.ReadWriter) {
			buf := make([]byte, 1024)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					n, err := rw.Read(buf)
					if n > 0 {
						rw.Write([]byte(strings.ToUpper(string(buf[:n]))))

					}
					if err != nil {
						return
					}
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

	// static peers
	c, err := New(p2, "/hello/1.0")
	if err != nil {
		t.Errorf("Client creation returned error `%s`", err.Error())
		return
	} else {
		respCh, err := c.New("hi", To(p1.ID())).Do()

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

		if v, k := res.Get("message"); k != nil || v.(string) != "HI" {
			t.Errorf("provider response does not match %v", v)
			return
		}

		str := "Yo!"
		if _, err := res.Write([]byte(str)); err != nil {
			t.Error(err)
			return
		} else {
			buf := make([]byte, len(str))
			_, err := res.Read(buf)
			if err != nil {
				t.Error(err)
				return
			}
			if strings.ToUpper(str) != string(buf) {
				t.Errorf("%s != %s", strings.ToUpper(str), string(buf))
				return
			}
		}

		// hi2
		respCh, err = c.New("hi2", To(p1.ID())).Do()

		if err != nil {
			t.Errorf("Command returned error `%s`", err.Error())
			return
		}

		res2 := <-respCh
		if res2 == nil {
			t.Error("Command timed out")
			return
		}
		defer res2.Close()
		if err := res2.Error(); err != nil {
			t.Errorf("error %s", err.Error())
			return
		}

		str = "Yo!"
		if _, err := res2.Write([]byte(str)); err != nil {
			t.Error(err)
			return
		} else {
			buf := make([]byte, 1024)
			n, err := res2.Read(buf)
			if err != nil {
				t.Error(err)
				return
			}
			if strings.ToUpper(str) != string(buf[:n]) {
				t.Errorf("%s != %s", strings.ToUpper(str), string(buf[:n]))
				return
			}
			_, err = res2.Read(buf)
			if err != io.EOF {
				t.Error("EOF failed")
				return
			}
		}

		// hi3
		respCh, err = c.New("hi3").Do()

		if err != nil {
			t.Errorf("Command returned error `%s`", err.Error())
			return
		}

		res3 := <-respCh
		if res3 == nil {
			t.Error("Command timed out")
			return
		}
		defer res3.Close()

		if err := res3.Error(); err != nil {
			t.Errorf("error %s", err.Error())
			return
		}

		// command with big argument
		base := "1234567890qwertyuiopasdfghjklzxcvbnm1234567890qwertyuiopasdfghjklzxcvbnm"
		var bigMessageBase string
		bigMessageCount := 32 * 1024 / len(base)
		for i := 0; i < bigMessageCount; i++ {
			bigMessageBase += base
		}

		if _, err := res3.Write([]byte(bigMessageBase)); err != nil {
			t.Error(err)
			return
		} else {
			res3.CloseWrite()

			buf := make([]byte, 1024)
			length := 0
			for {
				n, err := res3.Read(buf)
				if n > 0 {
					length += n
				}
				if err != nil {
					break
				}
			}
			if length != len(bigMessageBase) {
				t.Errorf("length does not match %d != %d", length, len(bigMessageBase))
				return
			}
		}

	}

	// Close
	c.Close()

}

func TestClientMultiSend(t *testing.T) {
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

	svr1, err := peerService.New(p1, "hello", "/hello/1.0")
	if err != nil {
		t.Errorf("Service creation returned error `%s`", err.Error())
		return
	}
	defer svr1.Stop()

	err = svr1.Define("hi", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"message": "HI"}, nil
	})
	if err != nil {
		t.Error(err)
		return
	}

	p2, err := peer.New( // provider
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n+200)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("Peer creation returned error `%s`", err.Error())
		return
	}
	defer p2.Close()

	svr2, err := peerService.New(p2, "hello", "/hello/1.0")
	if err != nil {
		t.Errorf("Service creation returned error `%s`", err.Error())
		return
	}
	defer svr2.Stop()

	err = svr2.Define("hi", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"message": "HI"}, nil
	})
	if err != nil {
		t.Error(err)
		return
	}

	p3, err := peer.New( // consumer
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", n+300)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("Ping test returned error `%s`", err.Error())
		return
	}
	defer p3.Close()

	err = p3.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()})
	if err != nil {
		t.Errorf("Connect to peer %v returned `%s`", p1.Peer().Addrs(), err.Error())
		return
	}

	err = p3.Peer().Connect(ctx, peercore.AddrInfo{ID: p2.ID(), Addrs: p2.Peer().Addrs()})
	if err != nil {
		t.Errorf("Connect to peer %v returned `%s`", p2.Peer().Addrs(), err.Error())
		return
	}

	// discover
	cd, err := New(p3, "/hello/1.0")
	if err != nil {
		t.Errorf("Client creation returned error `%s`", err.Error())
		return
	}

	resCh, err := cd.New("hi", Threshold(2)).Do()
	if err != nil {
		t.Errorf("Sending command returned error `%s`", err.Error())
		return
	}

	defer func() {
		for r := range resCh {
			r.Close()
		}
	}()

	count := 0

	for r := range resCh {
		if err := r.Error(); err != nil {
			t.Errorf("error %s", err.Error())
			return
		}
		if m, err := r.Get("message"); err != nil || m.(string) != "HI" {
			t.Errorf("node %s returned bad response `%v`", r.PID().String(), m)
			r.Close()
			return
		}
		count++
		r.Close()
	}

	if count != 2 {
		t.Errorf("MultiSending command failed with returns == %d", count)
	}

	cd.Close()
}
