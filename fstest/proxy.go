package fstest

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// TCPFaultDirection controls faults in one direction through a TCP proxy.
type TCPFaultDirection struct {
	// DisconnectAfter closes the write half after this many forwarded bytes.
	// Zero disables the cutoff.
	DisconnectAfter int64
	// Latency delays each socket read.
	Latency time.Duration
	// CorruptOffsets identifies absolute byte offsets to invert.
	CorruptOffsets []int64
}

// TCPFaultProxyOptions controls client-to-server and server-to-client faults.
type TCPFaultProxyOptions struct {
	ClientToServer TCPFaultDirection
	ServerToClient TCPFaultDirection
}

// TCPFaultProxy forwards TCP connections while injecting deterministic faults.
type TCPFaultProxy struct {
	listener  net.Listener
	upstream  string
	options   TCPFaultProxyOptions
	mu        sync.Mutex
	active    map[net.Conn]struct{}
	closed    bool
	wait      sync.WaitGroup
	closeOnce sync.Once
}

// NewTCPFaultProxy starts a loopback proxy for upstream.
func NewTCPFaultProxy(upstream string, options TCPFaultProxyOptions) (*TCPFaultProxy, error) {
	if _, _, err := net.SplitHostPort(upstream); err != nil {
		return nil, errors.New("fstest: TCP fault proxy upstream must include host and port")
	}
	if options.ClientToServer.DisconnectAfter < 0 || options.ServerToClient.DisconnectAfter < 0 {
		return nil, errors.New("fstest: TCP fault proxy cutoff must not be negative")
	}
	if options.ClientToServer.Latency < 0 || options.ServerToClient.Latency < 0 {
		return nil, errors.New("fstest: TCP fault proxy latency must not be negative")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &TCPFaultProxy{
		listener: listener,
		upstream: upstream,
		options:  options,
		active:   make(map[net.Conn]struct{}),
	}
	proxy.wait.Add(1)
	go proxy.accept()
	return proxy, nil
}

// Address returns the loopback listener address clients should dial.
func (p *TCPFaultProxy) Address() string { return p.listener.Addr().String() }

// Close stops accepting connections and closes active proxy streams.
func (p *TCPFaultProxy) Close() error {
	var closeErr error
	p.closeOnce.Do(func() {
		closeErr = p.listener.Close()
		p.mu.Lock()
		p.closed = true
		for connection := range p.active {
			_ = connection.Close()
		}
		p.mu.Unlock()
		p.wait.Wait()
	})
	if errors.Is(closeErr, net.ErrClosed) {
		return nil
	}
	return closeErr
}

func (p *TCPFaultProxy) accept() {
	defer p.wait.Done()
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.wait.Add(1)
		go p.forward(client)
	}
}

func (p *TCPFaultProxy) forward(client net.Conn) {
	defer p.wait.Done()
	server, err := net.Dial("tcp", p.upstream)
	if err != nil {
		_ = client.Close()
		return
	}
	if !p.track(client, server) {
		_ = client.Close()
		_ = server.Close()
		return
	}
	defer func() {
		_ = client.Close()
		_ = server.Close()
		p.untrack(client, server)
	}()

	done := make(chan struct{}, 2)
	go p.copyDirection(server, client, p.options.ClientToServer, done)
	go p.copyDirection(client, server, p.options.ServerToClient, done)
	<-done
	<-done
}

func (p *TCPFaultProxy) copyDirection(destination, source net.Conn, direction TCPFaultDirection, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	failAfter := int64(-1)
	if direction.DisconnectAfter > 0 {
		failAfter = direction.DisconnectAfter
	}
	reader := NewFaultReader(source, FaultReaderOptions{
		FailAfter:      failAfter,
		Err:            io.EOF,
		Latency:        direction.Latency,
		CorruptOffsets: direction.CorruptOffsets,
	})
	_, _ = io.Copy(destination, reader)
	if connection, ok := destination.(*net.TCPConn); ok {
		_ = connection.CloseWrite()
	} else {
		_ = destination.Close()
	}
}

func (p *TCPFaultProxy) track(connections ...net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	for _, connection := range connections {
		p.active[connection] = struct{}{}
	}
	return true
}

func (p *TCPFaultProxy) untrack(connections ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, connection := range connections {
		delete(p.active, connection)
	}
}
