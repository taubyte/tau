package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/p2p/peer"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/pkg/platformerr"

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

	inFlight sync.Map // peerCore.ID -> chan struct{}, one slot per call in flight
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
		// if no peers are provided, don't add any to the request
		if len(peers) > 0 {
			s.to = append(s.to, peers...)
			if s.threshold < len(s.to) {
				s.threshold = len(s.to)
			}
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
	slot chan struct{} // this call's in-flight slot; nil if it holds none
}

func (s stream) release() {
	if s.slot != nil {
		<-s.slot
	}
}

var (
	DiscoveryLimit int = 512

	DefaultMaxPeers    int = 16
	DefaultMaxParallel int = 64
	MaxStreamsPerSend  int = 16

	// MaxInFlightPerPeer caps one client's calls in flight to one peer. A peer
	// resets streams over its per-protocol-per-peer limit: 1024 for /tns/v1 and
	// /hoarder/v1 (p2p/helpers cloudLimits). 256 lets four clients of a protocol
	// on one node stay under that. Other protocols keep libp2p's defaults, about
	// 70, and only the retry below covers them.
	MaxInFlightPerPeer int = 256

	// LimitRetryDelay spaces retries of a call its peer refused for being over
	// the stream limit; each wait is random in [delay/2, delay).
	LimitRetryDelay time.Duration = 100 * time.Millisecond

	// PeerRetryInterval bounds how long a pending send can be stuck behind a
	// discovery gap (e.g. identify hasn't updated the peerstore yet, or a
	// backoff-cached lookup came back empty): recovery latency must beat
	// SendToPeerTimeout.
	PeerRetryInterval time.Duration = time.Second

	SendToPeerTimeout time.Duration = 10 * time.Second
	ConnectTimeout    time.Duration = 500 * time.Millisecond
)

// limitRetries bounds the retries of a call refused for the stream limit.
const limitRetries = 2

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

	retry := time.NewTicker(PeerRetryInterval)
	defer retry.Stop()

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

		case <-retry.C:
			// event-driven discovery only refreshes when a peerRequest
			// arrives; without this, a request parked here because identify
			// hadn't updated the peerstore yet (or FindPeers was served from
			// a poisoned backoff cache) would never be looked at again.
			if len(c.peerFeeds) == 0 {
				continue
			}

			kept := make([]*peerRequest, 0, len(c.peerFeeds))
			for _, pr := range c.peerFeeds {
				select {
				case <-pr.ctx.Done():
					close(pr.ch)
				default:
					kept = append(kept, pr)
				}
			}
			c.peerFeeds = kept

			c.refreshFromPeerStore()

			needMore := false
			for _, pr := range c.peerFeeds {
				if pr.need > len(c.activePeers) {
					needMore = true
					break
				}
			}
			if needMore && discoverDone {
				dpeers = c.discPeers()
				discoverDone = false
			}
		}
	}
}

// acquire waits for an in-flight slot to pid, for as long as ctx allows.
func (c *Client) acquire(ctx context.Context, pid peerCore.ID) (chan struct{}, error) {
	v, _ := c.inFlight.LoadOrStore(pid, make(chan struct{}, MaxInFlightPerPeer))
	slot := v.(chan struct{})
	select {
	case slot <- struct{}{}:
		return slot, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free stream to peer %s failed: %w", pid, ctx.Err())
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

	timeout := r.cmdTimeout
	if timeout == 0 {
		timeout = SendToPeerTimeout
	}
	deadline := time.Now().Add(timeout)

	if len(r.to) > 0 {
		ctx, ctxC := context.WithDeadline(r.client.ctx, deadline)
		defer ctxC()

		var lastErr error
		strms := make([]stream, 0, r.threshold)
		for _, pid := range r.to {
			if len(strms) >= r.threshold {
				break
			}
			slot, err := r.client.acquire(ctx, pid)
			if err != nil {
				lastErr = err
				continue
			}
			strm, err := r.client.openStream(pid)
			if err != nil {
				<-slot
				lastErr = err
				continue
			}
			strm.slot = slot
			strms = append(strms, strm)
		}
		if len(strms) == 0 {
			return nil, fmt.Errorf("no streams could be opened for command %q: %w", r.cmd, lastErr)
		}
		resCh, err := r.client.send(r.cmd, r.body, strms, r.threshold, deadline)
		if err != nil {
			for _, strm := range strms {
				strm.Reset()
				strm.release()
			}
		}
		return resCh, err
	}

	return r.client.send(r.cmd, r.body, nil, r.threshold, deadline)
}

func (r *Response) CloseRead() {
	if s, ok := r.ReadWriter.(network.Stream); ok {
		s.CloseRead()
	}
}

func (r *Response) CloseWrite() {
	if s, ok := r.ReadWriter.(network.Stream); ok {
		s.CloseWrite()
	}
}

func (r *Response) Close() {
	if s, ok := r.ReadWriter.(network.Stream); ok { // no stream if none could be opened
		s.Reset()
	}
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

	if err := peerError(strm.ID, cmdName, resp); err != nil {
		return &Response{
			ReadWriter: strm.Stream,
			pid:        strm.ID,
			err:        err,
		}
	}

	return &Response{
		ReadWriter: strm.Stream,
		pid:        strm.ID,
		Response:   resp,
	}
}

// limitExceeded reports whether the peer reset the stream for being over its
// stream limit. libp2p sends that code only before a stream reaches its handler
// (swarm_conn AcceptStream, basic_host newStreamHandler), so the peer has not
// seen the command and replaying any command is safe, writes included. A code
// from the local side, or an error the peer's handler returned, does not match.
func limitExceeded(err error) bool {
	var se *network.StreamError
	return errors.As(err, &se) && se.Remote && se.ErrorCode == network.StreamResourceLimitExceeded
}

// sendRetrying is sendTo, repeated on a fresh stream at most limitRetries times
// while the peer refuses the stream for being over its limit. It keeps the
// call's slot and deadline throughout.
func (c *Client) sendRetrying(ctx context.Context, strm stream, deadline time.Time, cmdName string, body command.Body) *Response {
	res := c.sendTo(strm, deadline, cmdName, body)
	for try := 0; try < limitRetries && limitExceeded(res.err); try++ {
		select {
		case <-ctx.Done():
			return res
		case <-time.After(LimitRetryDelay/2 + rand.N(LimitRetryDelay/2)):
		}
		next, err := c.openStream(strm.ID)
		if err != nil {
			return res
		}
		strm.Stream.Reset()
		strm.Stream = next.Stream
		res = c.sendTo(strm, deadline, cmdName, body)
	}
	return res
}

// peerError is the error a peer answered with, or nil.
func peerError(pid peerCore.ID, cmdName string, resp cr.Response) error {
	v, k := resp["error"]
	if !k {
		return nil
	}
	if code, ok := resp["code"].(string); ok && code != "" {
		return fmt.Errorf("peer %s returned error for command %q: %w", pid, cmdName, platformerr.New(platformerr.Code(code), errors.New(fmt.Sprint(v))))
	}
	return fmt.Errorf("peer %s returned error for command %q: %v", pid, cmdName, v)
}

func (c *Client) send(cmdName string, body command.Body, streams []stream, threshold int, deadline time.Time) (<-chan *Response, error) {
	select {
	case <-c.ctx.Done():
		return nil, errors.New("client context ended")
	default:
	}

	if threshold > MaxStreamsPerSend {
		return nil, fmt.Errorf("threshold %d exceeds MaxStreamsPerSend", threshold)
	}

	ctx, ctxC := context.WithDeadline(c.ctx, deadline)

	cmdDD, _ := ctx.Deadline()

	responses := make(chan *Response, MaxStreamsPerSend)
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
					slot, err := c.acquire(ctx, peer.ID)
					if err != nil {
						responses <- &Response{pid: peer.ID, err: err}
						return
					}
					strm, err := c.connect(peer)
					if err != nil {
						<-slot
						c.cleanPeers <- peer.ID
						continue morePeersLoop
					}
					strmsCount++
					select {
					case strms <- stream{Stream: strm, ID: peer.ID, slot: slot}:
					case <-ctx.Done():
						strm.Reset()
						<-slot
						return
					}
				}
			}
		}()
	} else {
		close(strms)
	}

	go func() {
		defer close(responses)
		defer ctxC()

		var wg sync.WaitGroup
		defer wg.Wait()

		for strm := range strms {
			wg.Add(1)
			go func(_strm stream) {
				defer wg.Done()
				defer _strm.release()
				select {
				case <-ctx.Done():
					responses <- &Response{
						ReadWriter: _strm.Stream,
						pid:        _strm.ID,
						err:        ctx.Err(),
					}
				case responses <- c.sendRetrying(ctx, _strm, cmdDD, cmdName, body):
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
