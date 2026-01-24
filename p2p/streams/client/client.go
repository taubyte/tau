package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/p2p/peer"
	cr "github.com/taubyte/tau/p2p/streams/command/response"

	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/network"
	peerCore "github.com/libp2p/go-libp2p/core/peer"
	protocol "github.com/libp2p/go-libp2p/core/protocol"

	"github.com/taubyte/tau/p2p/streams/command"
)

// SendOnlyClient defines the minimal interface needed for sending commands
type SendOnlyClient interface {
	Send(cmd string, body command.Body, peers ...peerCore.ID) (cr.Response, error)
}

type Client struct {
	ctx  context.Context
	ctxC context.CancelFunc

	node peer.Node
	path string
	tag  string

	activePeers map[peerCore.ID]*peerCore.AddrInfo

	maxPeers    int
	maxParallel int

	cleanPeers   chan peerCore.ID
	peerRequests chan *peerRequest
	peerFeeds    []*peerRequest
}

type peerRequest struct {
	ch   chan *peerCore.AddrInfo
	ctx  context.Context
	need int
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

type Option[T Client | Request] func(r *T) error

func Peers(max int) Option[Client] {
	return func(c *Client) error {
		c.maxPeers = max
		return nil
	}
}

func Parallel(max int) Option[Client] {
	return func(c *Client) error {
		c.maxParallel = max
		return nil
	}
}

func Timeout(timeout time.Duration) Option[Request] {
	return func(s *Request) error {
		s.cmdTimeout = timeout
		return nil
	}
}

func Body(body command.Body) Option[Request] {
	return func(s *Request) error {
		s.body = body
		return nil
	}
}

func Threshold(threshold int) Option[Request] {
	return func(s *Request) error {
		s.threshold = threshold
		return nil
	}
}

func To(peers ...peerCore.ID) Option[Request] {
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

func (r *Response) Error() error     { return r.err }
func (r *Response) PID() peerCore.ID { return r.pid }

type stream struct {
	network.Stream
	peerCore.ID
}

var (
	DiscoveryLimit int = 512

	DefaultMaxPeers    int = 16
	DefaultMaxParallel int = 64
	MaxStreamsPerSend  int = 16

	RefreshPeersInterval time.Duration = 30 * time.Second

	SendToPeerTimeout time.Duration = 10 * time.Second
	ConnectTimeout    time.Duration = 500 * time.Millisecond
)

func (c *Client) Context() context.Context { return c.ctx }

func (c *Client) Close() {
	c.ctxC()
}

func New(node peer.Node, path string, opts ...Option[Client]) (*Client, error) {
	ctx, cancel := context.WithCancel(node.Context())
	c := &Client{
		ctx:         ctx,
		ctxC:        cancel,
		node:        node,
		path:        path,
		maxPeers:    DefaultMaxPeers,
		maxParallel: DefaultMaxParallel,
		activePeers: make(map[peerCore.ID]*peerCore.AddrInfo),
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			cancel()
			return nil, err
		}
	}

	c.peerRequests = make(chan *peerRequest, c.maxParallel)
	c.cleanPeers = make(chan peerCore.ID, c.maxParallel)

	c.tag = fmt.Sprintf("/client/%p/%s", c, c.path)

	go c.discover()

	return c, nil
}

func (c *Client) New(cmd string, opts ...Option[Request]) *Request {
	r := &Request{
		client:    c,
		cmd:       cmd,
		to:        make([]peerCore.ID, 0),
		threshold: 1, // default: single send
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			r.err = err
			return r
		}
	}
	return r
}

func (c *Client) refreshFromPeerStore() {
	if len(c.activePeers) >= c.maxPeers {
		return
	}
	for _, pid := range c.node.Peer().Peerstore().Peers() {
		if len(pid) == 0 || c.node.ID() == pid {
			continue
		}
		if _, exist := c.activePeers[pid]; exist {
			continue
		}
		protos, err := c.node.Peer().Peerstore().GetProtocols(pid)
		if err != nil {
			continue
		}
		if slices.Contains(protos, protocol.ID(c.path)) {
			paddr := c.node.Peer().Peerstore().PeerInfo(pid)
			c.addPeer(&paddr)
		}
	}
}

func (c *Client) discPeers() <-chan peerCore.AddrInfo {
	discPeers, err := c.node.Discovery().FindPeers(c.ctx, c.path, discovery.Limit(DiscoveryLimit))
	if err != nil || discPeers == nil {
		empty := make(chan peerCore.AddrInfo)
		close(empty)
		return empty
	}
	return discPeers
}

func (c *Client) cleanPeer(pid peerCore.ID) {
	delete(c.activePeers, pid)
	c.node.Peer().ConnManager().Unprotect(pid, c.tag)
}

func (c *Client) addPeer(p *peerCore.AddrInfo) {
	c.activePeers[p.ID] = p
	c.node.Peer().ConnManager().Protect(p.ID, c.tag)

	kept := make([]*peerRequest, 0, len(c.peerFeeds))
	for _, pr := range c.peerFeeds {
		select {
		case <-pr.ctx.Done():
			close(pr.ch)
			continue
		default:
		}
		select {
		case pr.ch <- p:
			kept = append(kept, pr)
		case <-pr.ctx.Done():
			close(pr.ch)
		default:
			kept = append(kept, pr)
		}
	}
	c.peerFeeds = kept
}

func (c *Client) discover() {
	defer func() {
		for pid := range c.activePeers {
			c.cleanPeer(pid)
		}
		for _, pr := range c.peerFeeds {
			close(pr.ch)
		}
	}()

	c.refreshFromPeerStore()
	dpeers := c.discPeers()
	discoverDone := false
	dpeersWait := make(chan peerCore.AddrInfo)

	for {
		select {
		case <-c.ctx.Done():
			return

		case p, ok := <-dpeers:
			if !ok {
				discoverDone = true
				dpeers = dpeersWait
				continue
			}
			c.addPeer(&p)

		case pid := <-c.cleanPeers:
			c.cleanPeer(pid)

		case pr := <-c.peerRequests:
		peerLoop:
			for _, info := range c.activePeers {
				select {
				case pr.ch <- info:
				case <-pr.ctx.Done():
					close(pr.ch)
					pr = nil
					break peerLoop
				default:
				}
			}
			if pr == nil {
				continue
			}
			c.peerFeeds = append(c.peerFeeds, pr)

			if pr.need > len(c.activePeers) {
				c.refreshFromPeerStore()
				if pr.need > len(c.activePeers) && discoverDone {
					dpeers = c.discPeers()
					discoverDone = false
				}
			}
		}
	}
}

func (c *Client) openStream(pid peerCore.ID) (stream, error) {
	strm, err := c.node.Peer().NewStream(c.ctx, pid, protocol.ID(c.path))
	if err != nil {
		return stream{}, fmt.Errorf("peer new stream failed: %w", err)
	}
	return stream{Stream: strm, ID: pid}, nil
}

func (c *Client) peers(ctx context.Context, needs int) <-chan *peerCore.AddrInfo {
	ch := make(chan *peerCore.AddrInfo, c.maxPeers)
	select {
	case <-ctx.Done():
		close(ch)
		return ch
	case <-c.ctx.Done():
		close(ch)
		return ch
	default:
	}

	req := &peerRequest{ch: ch, ctx: ctx, need: needs}

	select {
	case <-ctx.Done():
		close(ch)
	case <-c.ctx.Done():
		close(ch)
	case c.peerRequests <- req:
	}
	return ch
}

func (c *Client) connect(p *peerCore.AddrInfo) (network.Stream, error) {
	switch c.node.Peer().Network().Connectedness(p.ID) {
	case network.Connected:
	case network.NotConnected:
		if err := c.node.Peer().Connect(
			network.WithDialPeerTimeout(c.ctx, ConnectTimeout),
			*p,
		); err != nil {
			return nil, fmt.Errorf("connecting to %s failed: %w", p.ID, err)
		}
	default:
		return nil, errors.New("unknown connection status")
	}

	strm, err := c.node.Peer().NewStream(
		network.WithNoDial(c.ctx, "application ensured connection exists"),
		p.ID,
		protocol.ID(c.path),
	)
	if err != nil {
		return nil, fmt.Errorf("new stream to %s failed: %w", p.ID, err)
	}
	return strm, nil
}

func (r *Request) Do() (<-chan *Response, error) {
	if r.err != nil {
		return nil, fmt.Errorf("request has error: %w", r.err)
	}

	select {
	case <-r.client.ctx.Done():
		return nil, fmt.Errorf("client context ended: %w", r.client.ctx.Err())
	default:
	}

	if len(r.to) > 0 {
		strms := make([]stream, 0, r.threshold)
		for _, pid := range r.to {
			if len(strms) >= r.threshold {
				break
			}
			strm, err := r.client.openStream(pid)
			if err != nil {
				continue
			}
			strms = append(strms, strm)
		}
		if len(strms) == 0 {
			return nil, fmt.Errorf("no streams could be opened for command %q", r.cmd)
		}
		return r.client.send(r.cmd, r.body, strms, r.threshold, r.cmdTimeout)
	}

	return r.client.send(r.cmd, r.body, nil, r.threshold, r.cmdTimeout)
}

func (r *Response) CloseRead() {
	r.ReadWriter.(network.Stream).CloseRead()
}

func (r *Response) CloseWrite() {
	r.ReadWriter.(network.Stream).CloseWrite()
}

func (r *Response) Close() {
	r.ReadWriter.(network.Stream).Reset()
}

func (c *Client) sendTo(strm stream, deadline time.Time, cmdName string, body command.Body) *Response {
	cmd := command.New(cmdName, body)

	if err := strm.SetWriteDeadline(deadline); err != nil {
		if !strings.Contains(err.Error(), "deadline not supported") {
			return &Response{
				ReadWriter: strm.Stream,
				pid:        strm.ID,
				err:        fmt.Errorf("setting write deadline failed with: %w", err),
			}
		}
	} else {
		defer strm.SetWriteDeadline(time.Time{})
	}

	if err := cmd.Encode(strm); err != nil {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("sending command `%s(%s)` failed with: %w", cmd.Command, c.path, err),
		}
	}

	if err := strm.SetReadDeadline(deadline); err != nil {
		if !strings.Contains(err.Error(), "deadline not supported") {
			return &Response{
				ReadWriter: strm.Stream,
				pid:        strm.ID,
				err:        fmt.Errorf("setting read deadline failed with: %w", err),
			}
		}
	} else {
		defer strm.SetReadDeadline(time.Time{})
	}

	resp, err := cr.Decode(strm)
	if err != nil {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("receive response of `%s(%s)` failed with: %w", cmd.Command, c.path, err),
		}
	}

	if v, k := resp["error"]; k {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("peer %s returned error for command %q: %v", strm.ID, cmdName, v),
		}
	}

	return &Response{
		ReadWriter: strm.Stream,
		pid:        strm.ID,
		Response:   resp,
	}
}

func (c *Client) send(cmdName string, body command.Body, streams []stream, threshold int, timeout time.Duration) (<-chan *Response, error) {
	select {
	case <-c.ctx.Done():
		return nil, errors.New("client context ended")
	default:
	}

	if timeout == 0 {
		timeout = SendToPeerTimeout
	}

	if threshold > MaxStreamsPerSend {
		return nil, fmt.Errorf("threshold %d exceeds MaxStreamsPerSend", threshold)
	}

	ctx, ctxC := context.WithTimeout(c.ctx, timeout)

	cmdDD, _ := ctx.Deadline()

	strms := make(chan stream, MaxStreamsPerSend)
	strmsCount := len(streams)

	needMoreStreams := true
	if len(streams) >= threshold {
		streams = streams[:threshold]
		strmsCount = threshold
		needMoreStreams = false
	}

	for _, strm := range streams {
		strms <- strm
	}

	if needMoreStreams {
		go func() {
			defer close(strms)

			discPeers := c.peers(ctx, threshold)

		morePeersLoop:
			for {
				if strmsCount >= threshold {
					return
				}
				select {
				case <-ctx.Done():
					return
				case peer := <-discPeers:
					if peer == nil {
						break morePeersLoop
					}
					strm, err := c.connect(peer)
					if err != nil {
						c.cleanPeers <- peer.ID
						continue morePeersLoop
					}
					strmsCount++
					select {
					case strms <- stream{Stream: strm, ID: peer.ID}:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	} else {
		close(strms)
	}

	responses := make(chan *Response, MaxStreamsPerSend)
	go func() {
		defer close(responses)
		defer ctxC()

		var wg sync.WaitGroup
		defer wg.Wait()

		for strm := range strms {
			wg.Add(1)
			go func(_strm stream) {
				defer wg.Done()
				select {
				case <-ctx.Done():
					responses <- &Response{
						ReadWriter: _strm.Stream,
						pid:        _strm.ID,
						err:        ctx.Err(),
					}
				case responses <- c.sendTo(_strm, cmdDD, cmdName, body):
				}
			}(strm)
		}
	}()

	return responses, nil
}

func (c *Client) syncSend(cmd string, opts ...Option[Request]) (cr.Response, error) {
	resCh, err := c.New(cmd, opts...).Do()
	if err != nil {
		return nil, fmt.Errorf("sending command %q failed: %w", cmd, err)
	}

	res := <-resCh
	if res == nil {
		return nil, fmt.Errorf("command %q timed out: %w", cmd, os.ErrDeadlineExceeded)
	}
	defer res.Close()

	if err := res.Error(); err != nil {
		return res.Response, fmt.Errorf("command %q returned error: %w", cmd, err)
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
