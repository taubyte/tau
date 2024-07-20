package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"errors"

	"golang.org/x/exp/slices"

	"github.com/taubyte/tau/p2p/peer"
	cr "github.com/taubyte/tau/p2p/streams/command/response"

	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/network"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"

	log "github.com/ipfs/go-log/v2"

	"github.com/taubyte/tau/p2p/streams/command"
)

type Client struct {
	ctx         context.Context
	ctxC        context.CancelFunc
	node        peer.Node
	path        string
	activePeers []peerCore.AddrInfo
	maxPeers    int
	morePeers   chan struct{}
	feedPeers   chan chan peerCore.AddrInfo
}

type Request struct {
	client     *Client
	to         []peerCore.ID
	cmd        string
	body       command.Body
	cmdTimeout time.Duration
	threshold  int
	err        error
}

type Option func(r *Request) error

func Timeout(timeout time.Duration) Option {
	return func(s *Request) error {
		s.cmdTimeout = timeout
		return nil
	}
}

func Body(body command.Body) Option {
	return func(s *Request) error {
		s.body = body
		return nil
	}
}

func Threshold(threshold int) Option {
	return func(s *Request) error {
		s.threshold = threshold
		return nil
	}
}

func To(peers ...peerCore.ID) Option {
	return func(s *Request) error {
		s.to = append(s.to, peers...)
		if s.threshold < len(s.to) {
			s.threshold = len(s.to)
		}
		return nil
	}
}

type Response struct {
	io.ReadWriter
	pid peerCore.ID
	cr.Response
	err error
}

func (r *Response) Error() error {
	return r.err
}

func (r *Response) PID() peerCore.ID {
	return r.pid
}

type stream struct {
	network.Stream
	peerCore.ID
}

type streamAsReadWriter struct {
	io.ReadWriter
}

func (rw streamAsReadWriter) Read(p []byte) (int, error) {
	n, err := rw.ReadWriter.Read(p)
	if err == network.ErrReset {
		err = io.EOF
	}
	return n, err
}

func (rw streamAsReadWriter) Write(p []byte) (int, error) {
	n, err := rw.ReadWriter.Write(p)
	if err == network.ErrReset {
		err = io.ErrClosedPipe
	}
	return n, err
}

var (
	NumConnectTries   int           = 3
	NumStreamers      int           = 3
	DiscoveryLimit    int           = 1024
	SendToPeerTimeout time.Duration = 10 * time.Second

	MaxStreamsPerSend = 16

	logger log.StandardLogger
)

func init() {
	logger = log.Logger("p2p.streams.client")
}

func (c *Client) Context() context.Context {
	return c.ctx
}

func New(node peer.Node, path string) (*Client, error) {
	c := &Client{
		node:      node,
		path:      path,
		maxPeers:  64,
		morePeers: make(chan struct{}),
		feedPeers: make(chan chan peerCore.AddrInfo, 64),
	}

	c.activePeers = make([]peerCore.AddrInfo, 0, c.maxPeers)

	c.ctx, c.ctxC = context.WithCancel(node.Context())

	return c, nil
}

func (c *Client) New(cmd string, opts ...Option) *Request {
	r := &Request{
		client:    c,
		cmd:       cmd,
		to:        make([]peerCore.ID, 0),
		threshold: 1, // default is single send
	}

	for _, opt := range opts {
		if err := opt(r); err != nil {
			r.err = err
			return r
		}
	}

	go c.discover()

	return r
}

func (c *Client) refreshFromPeerStore() {}
func (c *Client) discPeers() <-chan peerCore.AddrInfo {
	discPeers, err := c.node.Discovery().FindPeers(c.ctx, c.path, discovery.Limit(DiscoveryLimit))
	if err != nil || discPeers == nil {
		emptyChan := make(chan peerCore.AddrInfo)
		close(emptyChan)
		return emptyChan
	}
	return discPeers
}

func (c *Client) discoverMore() {
	select {
	case c.morePeers <- struct{}{}:
	default:
	}
}

func (c *Client) addPeer(any) {
}

func (c *Client) discover() {
	c.refreshFromPeerStore()
	dpeers := c.discPeers()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(30 * time.Second):
			c.refreshFromPeerStore()
		case p, ok := <-dpeers:
			if !ok {
				c.discoverMore()
			}
			c.addPeer(p)
		case <-c.morePeers:
			dpeers = c.discPeers()
		case ch := <-c.feedPeers:
			for _, p := range c.activePeers {
				ch <- p
			}
			close(ch)
		}
	}
}

// func (c *Client) peers() <-chan peerCore.AddrInfo {
// 	ret := make(chan peerCore.AddrInfo, c.maxPeers)
// 	c.feedPeers <- ret
// 	return ret
// }

func (r *Request) Do() (<-chan *Response, error) {
	if r.err != nil {
		return nil, r.err
	}

	if len(r.to) > 0 {
		strms := make([]stream, 0, r.threshold)
		for _, pid := range r.to {
			if len(strms) >= r.threshold {
				break
			}
			strm, err := r.client.openStream(pid)
			if err != nil {
				return nil, err
			}
			strms = append(strms, strm)
		}

		return r.client.send(r.cmd, r.body, strms, r.threshold, r.cmdTimeout)
	}

	return r.client.send(r.cmd, r.body, nil, r.threshold, r.cmdTimeout)
}

func (r *Response) CloseRead() {
	r.ReadWriter.(streamAsReadWriter).ReadWriter.(network.Stream).CloseRead()
}

func (r *Response) CloseWrite() {
	r.ReadWriter.(streamAsReadWriter).ReadWriter.(network.Stream).CloseWrite()
}

func (r *Response) Close() {
	r.ReadWriter.(streamAsReadWriter).ReadWriter.(network.Stream).Reset()
}

func (c *Client) openStream(pid peerCore.ID) (stream, error) {
	strm, err := c.node.Peer().NewStream(c.ctx, pid, protocol.ID(c.path))
	if err != nil {
		return stream{}, fmt.Errorf("peer new stream failed with: %w", err)
	}

	return stream{Stream: strm, ID: pid}, nil
}

func (c *Client) discoverO(ctx context.Context) <-chan peerCore.AddrInfo {
	storedPeers := c.node.Peer().Peerstore().Peers()
	cap := 32
	if len(storedPeers) > cap {
		cap = len(storedPeers)
	}

	peers := make(chan peerCore.AddrInfo, cap)

	go func() {
		defer close(peers)
		proto := protocol.ID(c.path)

		for _, peer := range storedPeers {
			if len(peer) == 0 || c.node.ID() == peer {
				continue
			}

			protos, err := c.node.Peer().Peerstore().GetProtocols(peer)
			if err != nil {
				logger.Errorf("getting protocols for `%s` failed with: %s", peer, err)
				continue
			}

			if slices.Contains(protos, proto) {
				peers <- peerCore.AddrInfo{ID: peer, Addrs: c.node.Peer().Peerstore().Addrs(peer)}
			}
		}

		if len(peers) == 0 {
			discPeers, err := c.node.Discovery().FindPeers(ctx, c.path, discovery.Limit(DiscoveryLimit))
			if err != nil {
				logger.Errorf("discovering nodes for `%s` failed with: %w", proto, err)
				return
			}

			nodeID := c.node.ID()
			for {
				select {
				case <-ctx.Done():
					return
				case peer := <-discPeers:
					if len(peer.ID) > 0 && peer.ID != nodeID {
						if len(peer.Addrs) == 0 {
							peer.Addrs = c.node.Peer().Peerstore().Addrs(peer.ID)
						}
						if len(peer.Addrs) > 0 {
							peers <- peer
						}
					}
				}
			}
		}
	}()

	return peers
}

func (c *Client) connect(peer peerCore.AddrInfo) (network.Stream, bool, error) {
	switch c.node.Peer().Network().Connectedness(peer.ID) {
	case network.Connected:
	case network.CanConnect, network.NotConnected:
		go c.node.Peer().Connect(c.ctx, peer)
		return nil, true, nil
	default:
		return nil, false, nil
	}

	strm, err := c.node.Peer().NewStream(
		network.WithNoDial(c.ctx, "application ensured connection to peer exists"),
		peer.ID,
		protocol.ID(c.path),
	)
	if err != nil {
		logger.Errorf("starting stream to `%s`;`%s` failed with: %w", peer.ID.String(), c.path, err)
		return nil, false, err
	}

	return strm, false, nil
}

func (c *Client) sendTo(strm stream, deadline time.Time, cmdName string, body command.Body) *Response {
	cmd := command.New(cmdName, body)
	rw := streamAsReadWriter{strm.Stream}

	if err := strm.SetWriteDeadline(deadline); err != nil {
		return &Response{
			ReadWriter: rw,
			pid:        strm.ID,
			err:        fmt.Errorf("setting write deadline failed with: %w", err),
		}
	}
	defer strm.SetWriteDeadline(time.Time{})

	if err := cmd.Encode(strm); err != nil {
		return &Response{
			ReadWriter: rw,
			pid:        strm.ID,
			err:        fmt.Errorf("sending command `%s(%s)` failed with: %w", cmd.Command, c.path, err),
		}
	}

	if err := strm.SetReadDeadline(deadline); err != nil {
		return &Response{
			ReadWriter: rw,
			pid:        strm.ID,
			err:        fmt.Errorf("setting read deadline failed with: %w", err),
		}
	}
	defer strm.SetReadDeadline(time.Time{})

	resp, err := cr.Decode(strm)
	if err != nil {
		return &Response{
			ReadWriter: rw,
			pid:        strm.ID,
			err:        fmt.Errorf("receive response of `%s(%s)` failed with: %w", cmd.Command, c.path, err),
		}
	}

	if v, k := resp["error"]; k {
		return &Response{
			ReadWriter: rw,
			pid:        strm.ID,
			err:        errors.New(fmt.Sprint(v)),
		}
	}

	return &Response{
		ReadWriter: rw,
		pid:        strm.ID,
		Response:   resp,
	}
}

func (c *Client) send(cmdName string, body command.Body, streams []stream, threshold int, timeout time.Duration) (<-chan *Response, error) {
	if timeout == 0 {
		timeout = SendToPeerTimeout
	}

	if threshold > MaxStreamsPerSend {
		return nil, fmt.Errorf("threshold %d exceeds MaxStreamsPerSend", threshold)
	}

	ctx, ctxC := context.WithTimeout(c.ctx, timeout)
	cmdDD, _ := ctx.Deadline()

	strms := make(chan stream, MaxStreamsPerSend)
	strmsCount := 0

	needMoreStreams := true
	if len(streams) > threshold {
		streams = streams[:threshold]
		needMoreStreams = false
	}

	for _, strm := range streams {
		strmsCount++
		strms <- strm
	}

	if needMoreStreams {
		discPeers := c.discoverO(ctx)
		go func() {
			defer close(strms)

			peers := make(chan peerCore.AddrInfo, MaxStreamsPerSend)
			defer close(peers)

			for {
				if strmsCount >= threshold {
					return
				}
				select {
				case peer, ok := <-discPeers:
					if ok {
						peers <- peer
					}
				case peer := <-peers:
					strm, repush, _ := c.connect(peer)
					if strm != nil && strmsCount < threshold {
						strmsCount++
						strms <- stream{Stream: strm, ID: peer.ID}
					}
					if repush {
						peers <- peer
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	} else {
		close(strms)
	}

	responses := make(chan *Response, MaxStreamsPerSend)
	go func() {
		var wg sync.WaitGroup
		defer func() {
			wg.Wait()
			close(responses)
			ctxC()
		}()
		for strm := range strms {
			wg.Add(1)
			go func(_strm stream) {
				defer wg.Done()
				responses <- c.sendTo(_strm, cmdDD, cmdName, body)
			}(strm)
		}
	}()

	return responses, nil
}

func (c *Client) Close() {
	c.ctxC()
}

func (c *Client) syncSend(cmd string, opts ...Option) (cr.Response, error) {
	resCh, err := c.New(cmd, opts...).Do()
	if err != nil {
		return nil, err
	}

	res := <-resCh
	if res == nil {
		return nil, os.ErrDeadlineExceeded
	}
	defer res.Close()

	if err := res.Error(); err != nil {
		return res.Response, err
	}

	return res.Response, nil
}

func (c *Client) Send(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error) {
	if len(peers) > 0 {
		return c.syncSend(cmd, Body(body), To(peers...), Threshold(len(peers)))
	} else {
		return c.syncSend(cmd, Body(body))
	}
}
