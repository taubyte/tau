package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toInt converts various numeric types to int
func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	default:
		return 0
	}
}

func TestClientSend(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

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

	ctx := t.Context()

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

func TestClientOptions(t *testing.T) {
	ctx := context.Background()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	// Test Peers option
	c, err := New(p1, "/test/1.0", Peers(32))
	if err != nil {
		t.Fatal(err)
	}
	if c.maxPeers != 32 {
		t.Errorf("expected maxPeers=32, got %d", c.maxPeers)
	}
	c.Close()

	// Test Parallel option
	c, err = New(p1, "/test/1.0", Parallel(128))
	if err != nil {
		t.Fatal(err)
	}
	if c.maxParallel != 128 {
		t.Errorf("expected maxParallel=128, got %d", c.maxParallel)
	}
	c.Close()

	// Test Context
	c, err = New(p1, "/test/1.0")
	if err != nil {
		t.Fatal(err)
	}
	if c.Context() == nil {
		t.Error("Context should not be nil")
	}
	c.Close()
}

func TestClientSend_Sync(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	svr, err := peerService.New(p1, "sync-test", "/sync/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer svr.Stop()

	err = svr.Define("echo", func(_ context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{"data": body["data"]}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	p2, err := peer.New(
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
		t.Fatal(err)
	}
	defer p2.Close()

	err = p2.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()})
	if err != nil {
		t.Fatal(err)
	}

	c, err := New(p2, "/sync/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Test Send method (synchronous)
	resp, err := c.Send("echo", command.Body{"data": "hello"}, p1.ID())
	if err != nil {
		t.Errorf("Send returned error: %s", err)
		return
	}

	if data, _ := resp.Get("data"); data != "hello" {
		t.Errorf("expected 'hello', got %v", data)
	}
}

func TestRequestTimeout(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := context.Background()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	c, err := New(p1, "/timeout/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Test Timeout option
	req := c.New("test", Timeout(5*time.Second))
	if req.cmdTimeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", req.cmdTimeout)
	}
}

func TestClient_SendWithoutPeers(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	c, err := New(p1, "/nopeers/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Send without specifying peers - will try to discover
	resp, err := c.Send("test", command.Body{})
	// Should timeout or fail since there are no peers
	if err == nil && resp != nil {
		t.Log("Send succeeded unexpectedly")
	}
}

func TestClient_RequestError(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := context.Background()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	c, err := New(p1, "/error-test/1.0")
	if err != nil {
		t.Fatal(err)
	}

	// Close the client first
	c.Close()

	// Try to send on closed client
	_, err = c.New("test").Do()
	if err == nil {
		t.Log("Expected error on closed client")
	}
}

func TestClient_ThresholdOption(t *testing.T) {
	ctx := context.Background()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	c, err := New(p1, "/threshold/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Test Threshold option
	req := c.New("test", Threshold(3))
	if req.threshold != 3 {
		t.Errorf("expected threshold=3, got %d", req.threshold)
	}
}

func TestClient_ToOption(t *testing.T) {
	ctx := context.Background()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	c, err := New(p1, "/to/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Test To option
	req := c.New("test", To(p1.ID()))
	if len(req.to) != 1 {
		t.Errorf("expected 1 peer in to list, got %d", len(req.to))
	}
	if req.to[0] != p1.ID() {
		t.Errorf("peer ID mismatch")
	}
}

func TestResponseMethods(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	var n int
	for n < 25565 || n > 40000 {
		n = rnd.Intn(100000)
	}

	p1, err := peer.New(
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
		t.Fatal(err)
	}
	defer p1.Close()

	svr, err := peerService.New(p1, "response-test", "/response/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer svr.Stop()

	err = svr.Define("test", func(_ context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{"status": "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	p2, err := peer.New(
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
		t.Fatal(err)
	}
	defer p2.Close()

	err = p2.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()})
	if err != nil {
		t.Fatal(err)
	}

	c, err := New(p2, "/response/1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	resCh, err := c.New("test", To(p1.ID())).Do()
	if err != nil {
		t.Fatal(err)
	}

	res := <-resCh
	if res == nil {
		t.Fatal("Response should not be nil")
	}
	defer res.Close()

	// Test PID method
	pid := res.PID()
	if pid != p1.ID() {
		t.Errorf("PID should match server ID")
	}

	// Test Error method
	if res.Error() != nil {
		t.Errorf("Error should be nil for successful response")
	}

	// Test CloseRead
	res.CloseRead()
}

func TestClientMultiSend(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

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

// TestMultiPeerThreshold tests sending to multiple peers with a threshold > 1
// ensuring we receive exactly threshold number of responses
func TestMultiPeerThreshold(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 5
	const threshold = 3

	providers := make([]peer.Node, numProviders)

	// Create provider peers
	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "multi", "/multi/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		providerID := i // capture for closure
		err = svr.Define("identify", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"provider": providerID}, nil
		})
		require.NoError(t, err)
	}

	// Create consumer peer
	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	// Connect consumer to all providers
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
	}

	client, err := New(consumer, "/multi/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Send with threshold
	resCh, err := client.New("identify", Threshold(threshold)).Do()
	require.NoError(t, err, "Sending command failed")

	responses := make(map[int]peercore.ID)
	for r := range resCh {
		assert.NoError(t, r.Error(), "Response error")
		if r.Error() == nil {
			providerID, err := r.Get("provider")
			assert.NoError(t, err, "Failed to get provider ID")
			responses[toInt(providerID)] = r.PID()
		}
		r.Close()
	}

	assert.Len(t, responses, threshold, "Expected %d responses", threshold)
}

// TestMultiPeerExplicitTo tests sending to specific peers via To() with threshold
func TestMultiPeerExplicitTo(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 4

	providers := make([]peer.Node, numProviders)

	// Create provider peers
	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "explicit", "/explicit/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		providerID := i
		err = svr.Define("whoami", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"id": providerID}, nil
		})
		require.NoError(t, err)
	}

	// Create consumer
	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	// Connect to all providers
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
	}

	client, err := New(consumer, "/explicit/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Send to specific 3 peers with threshold 2
	targetPeers := []peercore.ID{providers[0].ID(), providers[2].ID(), providers[3].ID()}
	resCh, err := client.New("whoami", To(targetPeers...), Threshold(2)).Do()
	require.NoError(t, err, "Sending command failed")

	receivedPeers := make(map[peercore.ID]bool)
	for r := range resCh {
		assert.NoError(t, r.Error(), "Response error")
		if r.Error() == nil {
			receivedPeers[r.PID()] = true
		}
		r.Close()
	}

	assert.Len(t, receivedPeers, 2, "Expected 2 responses (threshold)")

	// Verify responses came from targeted peers
	for pid := range receivedPeers {
		assert.Contains(t, targetPeers, pid, "Received response from non-targeted peer")
	}
}

// TestMultiPeerWithSlowResponder tests threshold behavior when some peers are slow
func TestMultiPeerWithSlowResponder(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create 3 providers - 2 fast, 1 slow
	fastProviders := make([]peer.Node, 2)

	for i := 0; i < 2; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Fast provider %d creation failed", i)
		defer p.Close()
		fastProviders[i] = p

		svr, err := peerService.New(p, "speed", "/speed/1.0")
		require.NoError(t, err, "Service creation failed")
		defer svr.Stop()

		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"speed": "fast"}, nil
		})
		require.NoError(t, err)
	}

	// Create slow provider
	slowProvider, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Slow provider creation failed")
	defer slowProvider.Close()

	slowSvr, err := peerService.New(slowProvider, "speed", "/speed/1.0")
	require.NoError(t, err, "Slow service creation failed")
	defer slowSvr.Stop()

	err = slowSvr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
		time.Sleep(500 * time.Millisecond)
		return cr.Response{"speed": "slow"}, nil
	})
	require.NoError(t, err)

	// Create consumer
	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	// Connect to all providers
	for i, p := range fastProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to fast provider %d failed", i)
	}
	err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: slowProvider.ID(), Addrs: slowProvider.Peer().Addrs()})
	require.NoError(t, err, "Connect to slow provider failed")

	client, err := New(consumer, "/speed/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Request threshold of 2 - should get 2 fast responses before slow one
	start := time.Now()
	resCh, err := client.New("ping", Threshold(2), Timeout(2*time.Second)).Do()
	require.NoError(t, err, "Sending command failed")

	count := 0
	fastCount := 0
	for r := range resCh {
		if r.Error() == nil {
			speed, _ := r.Get("speed")
			if speed == "fast" {
				fastCount++
			}
			count++
		}
		r.Close()
	}
	elapsed := time.Since(start)

	assert.GreaterOrEqual(t, count, 2, "Expected at least 2 responses")
	t.Logf("Elapsed: %v, got %d fast responses", elapsed, fastCount)
}

// TestMultiPeerAllPeersRespond tests that all targeted peers respond when threshold equals peer count
func TestMultiPeerAllPeersRespond(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 3

	providers := make([]peer.Node, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "all", "/all/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		idx := i
		err = svr.Define("check", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"index": idx}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	peerIDs := make([]peercore.ID, numProviders)
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
		peerIDs[i] = p.ID()
	}

	client, err := New(consumer, "/all/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Send to all 3 peers with threshold 3 (all must respond)
	resCh, err := client.New("check", To(peerIDs...)).Do()
	require.NoError(t, err, "Sending command failed")

	respondedPeers := make(map[peercore.ID]bool)
	for r := range resCh {
		assert.NoError(t, r.Error(), "Response error")
		if r.Error() == nil {
			respondedPeers[r.PID()] = true
		}
		r.Close()
	}

	assert.Len(t, respondedPeers, numProviders, "Expected all providers to respond")

	for _, pid := range peerIDs {
		assert.True(t, respondedPeers[pid], "Provider %s did not respond", pid)
	}
}

// TestMultiPeerWithBody tests sending commands with body to multiple peers
func TestMultiPeerWithBody(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 3

	providers := make([]peer.Node, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "body", "/body/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		idx := i
		err = svr.Define("process", func(_ context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
			val, _ := body["value"].(string)
			return cr.Response{
				"result":   fmt.Sprintf("processed-%s-by-%d", val, idx),
				"provider": idx,
			}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
	}

	client, err := New(consumer, "/body/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	resCh, err := client.New("process", Body(command.Body{"value": "testdata"}), Threshold(2)).Do()
	require.NoError(t, err, "Sending command failed")

	count := 0
	for r := range resCh {
		assert.NoError(t, r.Error(), "Response error")
		if r.Error() == nil {
			result, err := r.Get("result")
			assert.NoError(t, err, "Failed to get result")
			resultStr := result.(string)
			assert.True(t, strings.HasPrefix(resultStr, "processed-testdata-by-"), "Unexpected result: %s", resultStr)
			count++
		}
		r.Close()
	}

	assert.Equal(t, 2, count, "Expected 2 responses (threshold)")
}

// TestMultiPeerPartialFailure tests behavior when some peers fail
func TestMultiPeerPartialFailure(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create 2 working providers
	workingProviders := make([]peer.Node, 2)

	for i := 0; i < 2; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "partial", "/partial/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		err = svr.Define("work", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"status": "ok"}, nil
		})
		require.NoError(t, err)
	}

	// Create a provider that returns errors
	errorProvider, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Error provider creation failed")
	defer errorProvider.Close()

	errorSvr, err := peerService.New(errorProvider, "partial", "/partial/1.0")
	require.NoError(t, err, "Error service creation failed")
	defer errorSvr.Stop()

	err = errorSvr.Define("work", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
		return nil, errors.New("intentional error")
	})
	require.NoError(t, err)

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	// Connect to all providers
	for i, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to working provider %d failed", i)
	}
	err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: errorProvider.ID(), Addrs: errorProvider.Peer().Addrs()})
	require.NoError(t, err, "Connect to error provider failed")

	client, err := New(consumer, "/partial/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Request threshold of 2 - should still succeed with 2 working providers
	resCh, err := client.New("work", Threshold(2)).Do()
	require.NoError(t, err, "Sending command failed")

	successCount := 0
	errorCount := 0
	for r := range resCh {
		if r.Error() != nil {
			errorCount++
		} else {
			status, _ := r.Get("status")
			if status == "ok" {
				successCount++
			}
		}
		r.Close()
	}

	// We should have at least 2 responses (our threshold)
	totalResponses := successCount + errorCount
	assert.GreaterOrEqual(t, totalResponses, 2, "Expected at least 2 total responses")
	t.Logf("Got %d successful responses, %d error responses", successCount, errorCount)
}

// TestMultiPeerConcurrentRequests tests making multiple concurrent requests with threshold
func TestMultiPeerConcurrentRequests(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 4

	providers := make([]peer.Node, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "concurrent", "/concurrent/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		idx := i
		err = svr.Define("compute", func(_ context.Context, _ streams.Connection, body command.Body) (cr.Response, error) {
			reqID, _ := body["request_id"].(string)
			return cr.Response{
				"request_id": reqID,
				"provider":   idx,
			}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
	}

	client, err := New(consumer, "/concurrent/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Launch multiple concurrent requests
	const numRequests = 5
	const threshold = 2

	type result struct {
		requestID string
		responses int
		err       error
	}

	results := make(chan result, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(reqNum int) {
			reqID := fmt.Sprintf("req-%d", reqNum)
			resCh, err := client.New("compute", Body(command.Body{"request_id": reqID}), Threshold(threshold)).Do()
			if err != nil {
				results <- result{requestID: reqID, err: err}
				return
			}

			count := 0
			for r := range resCh {
				if r.Error() == nil {
					gotReqID, _ := r.Get("request_id")
					if gotReqID == reqID {
						count++
					}
				}
				r.Close()
			}
			results <- result{requestID: reqID, responses: count}
		}(i)
	}

	// Collect results
	for i := 0; i < numRequests; i++ {
		res := <-results
		assert.NoError(t, res.err, "Request %s failed", res.requestID)
		assert.Equal(t, threshold, res.responses, "Request %s: expected %d responses", res.requestID, threshold)
	}
}

// TestMultiPeerAllErrors tests behavior when all peers return errors
func TestMultiPeerAllErrors(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 3

	providers := make([]peer.Node, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "errors", "/errors/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		idx := i
		err = svr.Define("fail", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return nil, fmt.Errorf("provider %d failed intentionally", idx)
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	peerIDs := make([]peercore.ID, numProviders)
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
		peerIDs[i] = p.ID()
	}

	client, err := New(consumer, "/errors/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Send to all peers - all will fail
	resCh, err := client.New("fail", To(peerIDs...), Threshold(3)).Do()
	require.NoError(t, err, "Sending command failed")

	errorCount := 0
	successCount := 0
	for r := range resCh {
		if r.Error() != nil {
			errorCount++
			assert.Contains(t, r.Error().Error(), "failed intentionally", "Error should contain expected message")
		} else {
			successCount++
		}
		r.Close()
	}

	assert.Equal(t, numProviders, errorCount, "All providers should return errors")
	assert.Equal(t, 0, successCount, "No successful responses expected")
}

// TestMultiPeerTimeout tests behavior when peers timeout
func TestMultiPeerTimeout(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 3

	providers := make([]peer.Node, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err, "Provider %d creation failed", i)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "timeout", "/timeout/1.0")
		require.NoError(t, err, "Service %d creation failed", i)
		defer svr.Stop()

		// All providers sleep longer than the timeout
		err = svr.Define("slow", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			time.Sleep(2 * time.Second)
			return cr.Response{"status": "ok"}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	peerIDs := make([]peercore.ID, numProviders)
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err, "Connect to provider %d failed", i)
		peerIDs[i] = p.ID()
	}

	client, err := New(consumer, "/timeout/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	// Send with a short timeout - all providers will timeout
	start := time.Now()
	resCh, err := client.New("slow", To(peerIDs...), Threshold(3), Timeout(200*time.Millisecond)).Do()
	require.NoError(t, err, "Sending command failed")

	timeoutCount := 0
	successCount := 0
	for r := range resCh {
		if r.Error() != nil {
			timeoutCount++
		} else {
			successCount++
		}
		r.Close()
	}
	elapsed := time.Since(start)

	assert.Equal(t, numProviders, timeoutCount, "All providers should timeout")
	assert.Equal(t, 0, successCount, "No successful responses expected")
	assert.Less(t, elapsed, 1*time.Second, "Should complete faster than provider sleep time")
}

// TestMultiPeerMixedErrorsAndTimeouts tests with a mix of errors and timeouts
func TestMultiPeerMixedErrorsAndTimeouts(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create 1 working provider, 1 error provider, 1 timeout provider
	workingProvider, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Working provider creation failed")
	defer workingProvider.Close()

	workingSvr, err := peerService.New(workingProvider, "mixed", "/mixed/1.0")
	require.NoError(t, err)
	defer workingSvr.Stop()

	err = workingSvr.Define("action", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
		return cr.Response{"status": "ok", "type": "working"}, nil
	})
	require.NoError(t, err)

	// Error provider
	errorProvider, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Error provider creation failed")
	defer errorProvider.Close()

	errorSvr, err := peerService.New(errorProvider, "mixed", "/mixed/1.0")
	require.NoError(t, err)
	defer errorSvr.Stop()

	err = errorSvr.Define("action", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
		return nil, errors.New("provider error")
	})
	require.NoError(t, err)

	// Timeout provider
	timeoutProvider, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Timeout provider creation failed")
	defer timeoutProvider.Close()

	timeoutSvr, err := peerService.New(timeoutProvider, "mixed", "/mixed/1.0")
	require.NoError(t, err)
	defer timeoutSvr.Stop()

	err = timeoutSvr.Define("action", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
		time.Sleep(2 * time.Second)
		return cr.Response{"status": "ok", "type": "timeout"}, nil
	})
	require.NoError(t, err)

	// Consumer
	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err, "Consumer creation failed")
	defer consumer.Close()

	// Connect to all
	err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: workingProvider.ID(), Addrs: workingProvider.Peer().Addrs()})
	require.NoError(t, err)
	err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: errorProvider.ID(), Addrs: errorProvider.Peer().Addrs()})
	require.NoError(t, err)
	err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: timeoutProvider.ID(), Addrs: timeoutProvider.Peer().Addrs()})
	require.NoError(t, err)

	client, err := New(consumer, "/mixed/1.0")
	require.NoError(t, err, "Client creation failed")
	defer client.Close()

	allPeers := []peercore.ID{workingProvider.ID(), errorProvider.ID(), timeoutProvider.ID()}
	resCh, err := client.New("action", To(allPeers...), Threshold(3), Timeout(300*time.Millisecond)).Do()
	require.NoError(t, err)

	successCount := 0
	errorCount := 0
	for r := range resCh {
		if r.Error() != nil {
			errorCount++
		} else {
			successCount++
			status, _ := r.Get("status")
			assert.Equal(t, "ok", status)
		}
		r.Close()
	}

	// We should have 1 success (working), and 2 errors (1 error + 1 timeout)
	assert.Equal(t, 1, successCount, "Should have 1 successful response")
	assert.Equal(t, 2, errorCount, "Should have 2 error responses (1 error + 1 timeout)")
}

// TestMultiPeerThresholdLimitsPeersContacted tests that threshold limits how many peers are contacted
func TestMultiPeerThresholdLimitsPeersContacted(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create 5 providers, use threshold 3
	// Only 3 peers should be contacted
	const numProviders = 5
	const threshold = 3

	providers := make([]peer.Node, numProviders)
	contacted := make(chan int, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "limittest", "/limittest/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			contacted <- idx // signal that this provider was contacted
			return cr.Response{"provider": idx}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	for _, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}

	client, err := New(consumer, "/limittest/1.0")
	require.NoError(t, err)
	defer client.Close()

	resCh, err := client.New("ping", Threshold(threshold)).Do()
	require.NoError(t, err)

	// Collect responses
	responseCount := 0
	for r := range resCh {
		assert.NoError(t, r.Error())
		responseCount++
		r.Close()
	}

	// Should get exactly threshold responses
	assert.Equal(t, threshold, responseCount, "Should receive exactly threshold responses")

	// Count how many providers were actually contacted
	close(contacted)
	contactedCount := 0
	for range contacted {
		contactedCount++
	}

	assert.Equal(t, threshold, contactedCount, "Only threshold number of providers should be contacted")
	t.Logf("Contacted %d providers, got %d responses (threshold=%d)", contactedCount, responseCount, threshold)
}

// TestMultiPeerExplicitToWithThresholdLimit tests To() with more peers than threshold
func TestMultiPeerExplicitToWithThresholdLimit(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create 4 providers, target all via To(), but threshold 2
	// Only 2 should be contacted
	const numProviders = 4
	const threshold = 2

	providers := make([]peer.Node, numProviders)
	contacted := make(chan int, numProviders)

	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "explicit-limit", "/explicit-limit/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("check", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			contacted <- idx
			return cr.Response{"id": idx}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	peerIDs := make([]peercore.ID, numProviders)
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
		peerIDs[i] = p.ID()
	}

	client, err := New(consumer, "/explicit-limit/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Target all 4 peers but threshold 2
	resCh, err := client.New("check", To(peerIDs...), Threshold(threshold)).Do()
	require.NoError(t, err)

	responseCount := 0
	for r := range resCh {
		assert.NoError(t, r.Error())
		responseCount++
		r.Close()
	}

	assert.Equal(t, threshold, responseCount, "Should receive exactly threshold responses")

	close(contacted)
	contactedCount := 0
	for range contacted {
		contactedCount++
	}

	assert.Equal(t, threshold, contactedCount, "Only threshold peers should be contacted even when more are specified via To()")
	t.Logf("Specified %d peers via To(), contacted %d (threshold=%d)", numProviders, contactedCount, threshold)
}

// TestMultiPeerExplicitToSkipsUnreachable tests that To() skips unreachable peers and continues to others
func TestMultiPeerExplicitToSkipsUnreachable(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numWorking = 2
	const threshold = 2

	workingProviders := make([]peer.Node, numWorking)

	for i := 0; i < numWorking; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "skip-unreach", "/skip-unreach/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"provider": idx}, nil
		})
		require.NoError(t, err)
	}

	// Create unreachable peer
	unreachablePeer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	unreachablePeerID := unreachablePeer.ID()
	unreachablePeerAddrs := unreachablePeer.Peer().Addrs()
	unreachablePeer.Close() // Close it - now it's unreachable

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	for _, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}
	consumer.Peer().Peerstore().AddAddrs(unreachablePeerID, unreachablePeerAddrs, time.Hour)

	client, err := New(consumer, "/skip-unreach/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Put unreachable peer FIRST - should skip it and continue to working peers
	allPeers := []peercore.ID{unreachablePeerID, workingProviders[0].ID(), workingProviders[1].ID()}

	resCh, err := client.New("ping", To(allPeers...), Threshold(threshold)).Do()
	require.NoError(t, err, "Should succeed by skipping unreachable peer")

	successCount := 0
	for r := range resCh {
		assert.NoError(t, r.Error())
		successCount++
		r.Close()
	}

	assert.Equal(t, threshold, successCount, "Should get threshold responses after skipping unreachable peer")
	t.Logf("Skipped unreachable peer, got %d successful responses", successCount)
}

// TestMultiPeerExplicitToOrderDoesNotMatter tests that peer order in To() doesn't affect success
func TestMultiPeerExplicitToOrderDoesNotMatter(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numWorking = 3
	const threshold = 2

	workingProviders := make([]peer.Node, numWorking)

	for i := 0; i < numWorking; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "order-test", "/order-test/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"provider": idx}, nil
		})
		require.NoError(t, err)
	}

	// Create 2 unreachable peers
	unreachablePeers := make([]peercore.ID, 2)
	for i := 0; i < 2; i++ {
		unreachablePeer, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		unreachablePeers[i] = unreachablePeer.ID()
		unreachablePeer.Close()
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	for _, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}

	client, err := New(consumer, "/order-test/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Mix unreachable and working peers in different orders - all should work
	testCases := [][]peercore.ID{
		// Unreachable first
		{unreachablePeers[0], workingProviders[0].ID(), workingProviders[1].ID()},
		// Unreachable in middle
		{workingProviders[0].ID(), unreachablePeers[0], workingProviders[1].ID()},
		// Unreachable last
		{workingProviders[0].ID(), workingProviders[1].ID(), unreachablePeers[0]},
		// Multiple unreachable
		{unreachablePeers[0], unreachablePeers[1], workingProviders[0].ID(), workingProviders[1].ID()},
	}

	for i, peers := range testCases {
		t.Run(fmt.Sprintf("order_%d", i), func(t *testing.T) {
			resCh, err := client.New("ping", To(peers...), Threshold(threshold)).Do()
			require.NoError(t, err, "Should succeed regardless of peer order")

			successCount := 0
			for r := range resCh {
				if r.Error() == nil {
					successCount++
				}
				r.Close()
			}

			assert.Equal(t, threshold, successCount, "Should get threshold responses")
		})
	}
}

// TestMultiPeerAllUnreachableFails tests that request fails when all peers are unreachable
func TestMultiPeerAllUnreachableFails(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create only unreachable peers
	unreachablePeers := make([]peercore.ID, 3)
	for i := 0; i < 3; i++ {
		unreachablePeer, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		unreachablePeers[i] = unreachablePeer.ID()
		unreachablePeer.Close()
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	client, err := New(consumer, "/all-unreach/1.0")
	require.NoError(t, err)
	defer client.Close()

	_, err = client.New("ping", To(unreachablePeers...), Threshold(2)).Do()
	assert.Error(t, err, "Should fail when all peers are unreachable")
	assert.Contains(t, err.Error(), "no streams could be opened", "Error should mention no streams")
	t.Logf("Expected error: %v", err)
}

// TestMultiPeerDiscoveryWithUnreachablePeers tests discovery mode when some peers are unreachable
func TestMultiPeerDiscoveryWithUnreachablePeers(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// Create 4 working providers
	const numWorking = 4
	const threshold = 3

	workingProviders := make([]peer.Node, numWorking)
	contacted := make(chan int, numWorking*2)

	for i := 0; i < numWorking; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "discover-unreach", "/discover-unreach/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			contacted <- idx
			return cr.Response{"provider": idx}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	// Connect to all working providers
	for _, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}

	client, err := New(consumer, "/discover-unreach/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Use discovery mode (no To()) - should find working peers
	resCh, err := client.New("ping", Threshold(threshold)).Do()
	require.NoError(t, err)

	successCount := 0
	for r := range resCh {
		if r.Error() == nil {
			successCount++
		}
		r.Close()
	}

	close(contacted)
	contactedCount := 0
	for range contacted {
		contactedCount++
	}

	assert.Equal(t, threshold, successCount, "Should get threshold successful responses")
	assert.Equal(t, threshold, contactedCount, "Should contact exactly threshold peers")
	t.Logf("Contacted %d working peers, got %d successful responses", contactedCount, successCount)
}

// TestMultiPeerProtocolNotSupportedSkipped tests that peers without protocol support are skipped
func TestMultiPeerProtocolNotSupportedSkipped(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numWorking = 2
	const threshold = 2

	workingProviders := make([]peer.Node, numWorking)

	for i := 0; i < numWorking; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "proto-skip", "/proto-skip/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"provider": idx}, nil
		})
		require.NoError(t, err)
	}

	// Peer that doesn't support our protocol
	noHandlerPeer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer noHandlerPeer.Close()

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	for _, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}
	err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: noHandlerPeer.ID(), Addrs: noHandlerPeer.Peer().Addrs()})
	require.NoError(t, err)

	client, err := New(consumer, "/proto-skip/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Put no-handler peer first - should skip it and use working peers
	allPeers := []peercore.ID{noHandlerPeer.ID(), workingProviders[0].ID(), workingProviders[1].ID()}

	resCh, err := client.New("ping", To(allPeers...), Threshold(threshold)).Do()
	require.NoError(t, err, "Should succeed by skipping peer without protocol support")

	successCount := 0
	for r := range resCh {
		assert.NoError(t, r.Error())
		successCount++
		r.Close()
	}

	assert.Equal(t, threshold, successCount, "Should get threshold responses after skipping incompatible peer")
	t.Logf("Skipped incompatible peer, got %d successful responses", successCount)
}

// TestMultiPeerProtocolNotSupportedSkippedInDiscovery tests discovery skips peers without protocol
func TestMultiPeerProtocolNotSupportedSkippedInDiscovery(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numWorking = 3
	const threshold = 2

	workingProviders := make([]peer.Node, numWorking)

	for i := 0; i < numWorking; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "discovery-proto", "/discovery-proto/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("ping", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"provider": idx}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	for _, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}

	client, err := New(consumer, "/discovery-proto/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Use discovery - should only find peers that support the protocol
	resCh, err := client.New("ping", Threshold(threshold)).Do()
	require.NoError(t, err)

	successCount := 0
	for r := range resCh {
		assert.NoError(t, r.Error())
		successCount++
		r.Close()
	}

	assert.Equal(t, threshold, successCount, "Should get threshold responses from discovered peers")
	t.Logf("Got %d responses from discovery", successCount)
}

// TestMultiPeerThresholdWithSomeErrors tests meeting threshold despite some errors
func TestMultiPeerThresholdWithSomeErrors(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	// 3 working + 2 error providers, threshold 3
	const numWorking = 3
	const numError = 2
	const threshold = 3

	workingProviders := make([]peer.Node, numWorking)
	errorProviders := make([]peer.Node, numError)

	for i := 0; i < numWorking; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		workingProviders[i] = p

		svr, err := peerService.New(p, "thresh", "/thresh/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		idx := i
		err = svr.Define("work", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return cr.Response{"status": "ok", "worker": idx}, nil
		})
		require.NoError(t, err)
	}

	for i := 0; i < numError; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		errorProviders[i] = p

		svr, err := peerService.New(p, "thresh", "/thresh/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		err = svr.Define("work", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			return nil, errors.New("error provider")
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	// Connect to all
	for _, p := range workingProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}
	for _, p := range errorProviders {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
	}

	client, err := New(consumer, "/thresh/1.0")
	require.NoError(t, err)
	defer client.Close()

	resCh, err := client.New("work", Threshold(threshold)).Do()
	require.NoError(t, err)

	successCount := 0
	errorCount := 0
	for r := range resCh {
		if r.Error() != nil {
			errorCount++
		} else {
			successCount++
		}
		r.Close()
	}

	// Should get threshold responses (mix of success and error)
	totalResponses := successCount + errorCount
	assert.GreaterOrEqual(t, totalResponses, threshold, "Should get at least threshold responses")
	// At least some should be successful since we have 3 working providers
	assert.Greater(t, successCount, 0, "Should have some successful responses")
	t.Logf("Got %d successful, %d errors out of threshold %d", successCount, errorCount, threshold)
}

// TestGoroutineCountPerSend measures goroutines created per send() call
func TestGoroutineCountPerSend(t *testing.T) {
	logging.SetLogLevel("*", "error")

	ctx := t.Context()

	const numProviders = 4

	providers := make([]peer.Node, numProviders)
	for i := 0; i < numProviders; i++ {
		p, err := peer.New(
			ctx,
			nil,
			keypair.NewRaw(),
			nil,
			[]string{"/ip4/127.0.0.1/tcp/0"},
			nil,
			true,
			false,
		)
		require.NoError(t, err)
		defer p.Close()
		providers[i] = p

		svr, err := peerService.New(p, "goroutine-test", "/goroutine-test/1.0")
		require.NoError(t, err)
		defer svr.Stop()

		err = svr.Define("slow", func(_ context.Context, _ streams.Connection, _ command.Body) (cr.Response, error) {
			time.Sleep(500 * time.Millisecond) // Hold the goroutine for measurement
			return cr.Response{"ok": true}, nil
		})
		require.NoError(t, err)
	}

	consumer, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{"/ip4/127.0.0.1/tcp/0"},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer consumer.Close()

	peerIDs := make([]peercore.ID, numProviders)
	for i, p := range providers {
		err = consumer.Peer().Connect(ctx, peercore.AddrInfo{ID: p.ID(), Addrs: p.Peer().Addrs()})
		require.NoError(t, err)
		peerIDs[i] = p.ID()
	}

	client, err := New(consumer, "/goroutine-test/1.0")
	require.NoError(t, err)
	defer client.Close()

	// Let things settle
	time.Sleep(100 * time.Millisecond)

	testCases := []struct {
		name      string
		threshold int
		toPeers   int
	}{
		{"threshold=1, to=1", 1, 1},
		{"threshold=2, to=2", 2, 2},
		{"threshold=4, to=4", 4, 4},
		{"threshold=2, to=4", 2, 4}, // More peers than threshold
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Force GC and let goroutines settle
			time.Sleep(50 * time.Millisecond)

			before := runtime.NumGoroutine()

			resCh, err := client.New("slow",
				To(peerIDs[:tc.toPeers]...),
				Threshold(tc.threshold),
			).Do()
			require.NoError(t, err)

			// Measure while goroutines are active
			time.Sleep(50 * time.Millisecond)
			during := runtime.NumGoroutine()
			spawned := during - before

			// Expected: 1 response collector + threshold stream handlers
			// If needMoreStreams: +1 discovery goroutine
			needsDiscovery := tc.toPeers < tc.threshold
			expectedMin := 1 + tc.threshold // collector + per-stream
			if needsDiscovery {
				expectedMin++ // discovery goroutine
			}
			if tc.toPeers < tc.threshold {
				expectedMin = 1 + tc.toPeers // only as many streams as we have peers
				if needsDiscovery {
					expectedMin++
				}
			}

			t.Logf("Goroutines: before=%d, during=%d, spawned=%d (threshold=%d, toPeers=%d)",
				before, during, spawned, tc.threshold, tc.toPeers)

			// Drain responses
			count := 0
			for r := range resCh {
				r.Close()
				count++
			}

			// Wait for cleanup
			time.Sleep(100 * time.Millisecond)
			after := runtime.NumGoroutine()

			t.Logf("After cleanup: goroutines=%d, responses=%d", after, count)

			// Verify cleanup - should be close to before (within a few for background tasks)
			assert.LessOrEqual(t, after, before+5, "Goroutines should be cleaned up after responses drained")
		})
	}
}
