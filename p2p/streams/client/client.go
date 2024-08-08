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

	"github.com/taubyte/tau/p2p/streams/command"
)

type Client struct {
	ctx         context.Context
	ctxC        context.CancelFunc
	node        peer.Node
	path        string
	tag         string
	activePeers map[peerCore.ID]*peerCore.AddrInfo
	maxPeers    int
	maxParallel int
	morePeers   chan int
	cleanPeers  chan peerCore.ID
	feedPeers   chan chan *peerCore.AddrInfo
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

// client option
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

// request options
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
	DefaultMaxPeers    int = 16
	DefaultMaxParallel int = 64
	MaxStreamsPerSend  int = 16

	RefreshPeersInterval time.Duration = 30 * time.Second

	SendToPeerTimeout time.Duration = 10 * time.Second
	ConnectTimeout    time.Duration = 500 * time.Millisecond

	//logger log.StandardLogger
)

// func init() {
// 	logger = log.Logger("p2p.streams.client")
// }

func (c *Client) Context() context.Context {
	return c.ctx
}

func (c *Client) Close() {
	c.ctxC()
}

func New(node peer.Node, path string, opts ...Option[Client]) (*Client, error) {
	c := &Client{
		node:        node,
		path:        path,
		maxPeers:    DefaultMaxPeers,
		maxParallel: DefaultMaxParallel,
		activePeers: make(map[peerCore.ID]*peerCore.AddrInfo),
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	c.morePeers, c.feedPeers, c.cleanPeers = make(chan int, c.maxParallel), make(chan chan *peerCore.AddrInfo, c.maxParallel), make(chan peerCore.ID, c.maxParallel)

	c.ctx, c.ctxC = context.WithCancel(node.Context())

	c.tag = fmt.Sprintf("/client/%p/%s", c, c.path)

	go c.discover()

	return c, nil
}

func (c *Client) New(cmd string, opts ...Option[Request]) *Request {
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

	return r
}

func (c *Client) refreshFromPeerStore() {
	if len(c.activePeers) < c.maxPeers {
		for _, peer := range c.node.Peer().Peerstore().Peers() {
			if len(peer) == 0 || c.node.ID() == peer {
				continue
			}

			if _, exist := c.activePeers[peer]; exist {
				continue
			}

			protos, err := c.node.Peer().Peerstore().GetProtocols(peer)
			if err != nil {
				continue
			}

			if slices.Contains(protos, protocol.ID(c.path)) {
				paddr := c.node.Peer().Peerstore().PeerInfo(peer)
				c.addPeer(&paddr)
			}
		}
	}
}

func (c *Client) discPeers() <-chan peerCore.AddrInfo {
	discPeers, err := c.node.Discovery().FindPeers(c.ctx, c.path, discovery.Limit(c.maxPeers))
	if err != nil || discPeers == nil {
		emptyChan := make(chan peerCore.AddrInfo)
		close(emptyChan)
		return emptyChan
	}
	return discPeers
}

func (c *Client) close() {
	time.Sleep(SendToPeerTimeout) // give time for all requests to be done
	for pid := range c.activePeers {
		c.cleanPeer(pid)
	}
	close(c.feedPeers)
	close(c.morePeers)
}

func (c *Client) cleanPeer(peer peerCore.ID) {
	delete(c.activePeers, peer)
	c.node.Peer().ConnManager().Unprotect(peer, c.tag)
}

func (c *Client) addPeer(peer *peerCore.AddrInfo) {
	c.activePeers[peer.ID] = peer
	c.node.Peer().ConnManager().Protect(peer.ID, c.tag)
}

func (c *Client) discover() {
	c.refreshFromPeerStore()
	dpeersWait := make(chan peerCore.AddrInfo)
	defer close(dpeersWait)
	dpeers := c.discPeers()
	discoverDone := false
	for {
		select {
		case <-c.ctx.Done():
			c.close()
			return
		case <-time.After(RefreshPeersInterval):
			c.refreshFromPeerStore()
		case p, ok := <-dpeers:
			if !ok {
				discoverDone = true
				dpeers = dpeersWait
			} else {
				c.addPeer(&p)
			}
		case need := <-c.morePeers:
			if need > len(c.activePeers) && discoverDone {
				dpeers = c.discPeers()
			}
		case peer := <-c.cleanPeers:
			c.cleanPeer(peer)
		case ch := <-c.feedPeers:
			for _, p := range c.activePeers {
				ch <- p
			}
			close(ch)
		}
	}
}

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

func (c *Client) peers(ctx context.Context, needs int) <-chan *peerCore.AddrInfo {
	ch := make(chan *peerCore.AddrInfo, c.maxPeers)
	select {
	case c.morePeers <- needs:
		fmt.Println("ok 1")
	case <-ctx.Done():
		fmt.Println("Woops 1")
		close(ch)
		return ch
	}

	select {
	case c.feedPeers <- ch:
		fmt.Println("ok 2")
	case <-ctx.Done():
		fmt.Println("Woops 2")
		close(ch)
		return ch
	}

	fmt.Println("go")
	return ch
}

func (c *Client) connect(peer *peerCore.AddrInfo) (network.Stream, error) {
	switch c.node.Peer().Network().Connectedness(peer.ID) {
	case network.Connected:
	case network.CanConnect, network.NotConnected:
		err := c.node.Peer().Connect(
			network.WithDialPeerTimeout(c.ctx, ConnectTimeout),
			*peer,
		)
		if err != nil {
			return nil, fmt.Errorf("connecting to %s failed with %w", peer.ID.String(), err)
		}
	case network.CannotConnect:
		return nil, errors.New("can't connect")
	default:
		return nil, errors.New("unknown connection status")
	}

	strm, err := c.node.Peer().NewStream(
		network.WithNoDial(c.ctx, "application ensured connection to peer exists"),
		peer.ID,
		protocol.ID(c.path),
	)
	if err != nil {
		return nil, fmt.Errorf("new stream to %s failed with %w", peer.ID.String(), err)
	}

	return strm, nil
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
		discPeers := c.peers(ctx, threshold)
		go func() {
			defer close(strms)

		morePeersLoop:
			for {
				if strmsCount >= threshold {
					return
				}
				select {
				case peer := <-discPeers:
					fmt.Println("dico>", peer)
					if peer == nil {
						break morePeersLoop
					}
					strm, _ := c.connect(peer)
					if strm != nil && strmsCount < threshold {
						strmsCount++
						strms <- stream{Stream: strm, ID: peer.ID}
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
		defer close(responses)
		defer wg.Wait()
		defer ctxC()
		for strm := range strms {
			fmt.Println("stream:", strm)
			wg.Add(1)
			go func(_strm stream) {
				defer wg.Done()
				responses <- c.sendTo(_strm, cmdDD, cmdName, body)
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
