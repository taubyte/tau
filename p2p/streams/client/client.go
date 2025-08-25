package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"errors"

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

// Client options
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

// Request options
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

// Lifecycle methods
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

	// Size internal queues based on configured parallelism
	c.peerRequests = make(chan *peerRequest, c.maxParallel)
	c.cleanPeers = make(chan peerCore.ID, c.maxParallel)

	c.tag = fmt.Sprintf("/client/%p/%s", c, c.path)

	// Start peer discovery / brokering
	go c.discover()

	return c, nil
}

// Request builder
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

// Peer discovery / brokering (single goroutine)
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
		// Do NOT close c.peerRequests or c.cleanPeers to avoid panics from concurrent senders
	}()

	c.refreshFromPeerStore()
	dpeers := c.discPeers()
	discoverDone := false
	// a never-ready channel to disable the discovery case when exhausted
	dpeersWait := make(chan peerCore.AddrInfo)

	for {
		select {
		case <-c.ctx.Done():
			return

		case p, ok := <-dpeers:
			if !ok {
				discoverDone = true
				// replace with a never-ready channel so the select case doesn't spin
				dpeers = dpeersWait
				continue
			}
			c.addPeer(&p)

		case pid := <-c.cleanPeers:
			c.cleanPeer(pid)

		case pr := <-c.peerRequests:
			// Feed existing peers immediately
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

			// If request needs more peers than we have, try to source more
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

// Peer access helpers
func (c *Client) openStream(pid peerCore.ID) (stream, error) {
	strm, err := c.node.Peer().NewStream(c.ctx, pid, protocol.ID(c.path))
	if err != nil {
		return stream{}, fmt.Errorf("failed to create stream: %w", err)
	}
	return stream{Stream: strm, ID: pid}, nil
}

// peers returns a channel that will be fed with up to "needs" peer infos,
// or closed early if ctx/c.ctx is done
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
			return nil, fmt.Errorf("failed to connect to %s: %w", p.ID, err)
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
		return nil, fmt.Errorf("failed to create stream to %s: %w", p.ID, err)
	}
	return strm, nil
}

// Sending methods
func (r *Request) Do() (<-chan *Response, error) {
	if r.err != nil {
		return nil, r.err
	}

	// Check if client is closed using context
	select {
	case <-r.client.ctx.Done():
		return nil, os.ErrClosed
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
				return nil, err
			}
			strms = append(strms, strm)
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
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("failed to set write deadline: %w", err),
		}
	}
	defer strm.SetWriteDeadline(time.Time{})

	if err := cmd.Encode(strm); err != nil {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("failed to send command `%s`: %w", cmd.Command, err),
		}
	}

	if err := strm.SetReadDeadline(deadline); err != nil {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("failed to set read deadline: %w", err),
		}
	}
	defer strm.SetReadDeadline(time.Time{})

	resp, err := cr.Decode(strm)
	if err != nil {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        fmt.Errorf("failed to receive response: %w", err),
		}
	}

	if v, k := resp["error"]; k {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        errors.New(fmt.Sprint(v)),
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
		return nil, os.ErrClosed
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
					} else if strmsCount < threshold {
						strmsCount++
						select {
						case strms <- stream{Stream: strm, ID: peer.ID}:
						case <-ctx.Done():
							return
						}
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
						ReadWriter: strm.Stream,
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
