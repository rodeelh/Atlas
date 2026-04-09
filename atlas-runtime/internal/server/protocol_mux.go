package server

import (
	"bufio"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

type protocolMux struct {
	base    net.Listener
	httpLn  *connListener
	tlsLn   *connListener
	closeMu sync.Once
}

func NewProtocolMux(base net.Listener) *protocolMux {
	mux := &protocolMux{
		base:   base,
		httpLn: newConnListener(base.Addr()),
		tlsLn:  newConnListener(base.Addr()),
	}
	go mux.serve()
	return mux
}

func (m *protocolMux) HTTPListener() net.Listener {
	return m.httpLn
}

func (m *protocolMux) TLSListener() net.Listener {
	return m.tlsLn
}

func (m *protocolMux) Close() error {
	var err error
	m.closeMu.Do(func() {
		err = m.base.Close()
		m.httpLn.closeWithErr(net.ErrClosed)
		m.tlsLn.closeWithErr(net.ErrClosed)
	})
	return err
}

func (m *protocolMux) serve() {
	for {
		conn, err := m.base.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				m.httpLn.closeWithErr(net.ErrClosed)
				m.tlsLn.closeWithErr(net.ErrClosed)
				return
			}
			m.httpLn.closeWithErr(err)
			m.tlsLn.closeWithErr(err)
			return
		}
		go m.dispatch(conn)
	}
}

func (m *protocolMux) dispatch(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(conn)
	peek, err := reader.Peek(1)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		return
	}
	wrapped := &peekConn{Conn: conn, reader: reader}
	if len(peek) > 0 && peek[0] == 0x16 {
		if err := m.tlsLn.push(wrapped); err != nil {
			_ = conn.Close()
		}
		return
	}
	if err := m.httpLn.push(wrapped); err != nil {
		_ = conn.Close()
	}
}

type peekConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *peekConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type connListener struct {
	addr   net.Addr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	errMu  sync.Mutex
	err    error
}

func newConnListener(addr net.Addr) *connListener {
	return &connListener{
		addr:   addr,
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *connListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		if conn == nil {
			return nil, l.closeErr()
		}
		return conn, nil
	case <-l.closed:
		return nil, l.closeErr()
	}
}

func (l *connListener) Close() error {
	l.closeWithErr(net.ErrClosed)
	return nil
}

func (l *connListener) Addr() net.Addr {
	return l.addr
}

func (l *connListener) push(conn net.Conn) error {
	select {
	case l.conns <- conn:
		return nil
	case <-l.closed:
		return io.ErrClosedPipe
	}
}

func (l *connListener) closeWithErr(err error) {
	l.once.Do(func() {
		l.errMu.Lock()
		l.err = err
		l.errMu.Unlock()
		close(l.closed)
	})
}

func (l *connListener) closeErr() error {
	l.errMu.Lock()
	defer l.errMu.Unlock()
	if l.err != nil {
		return l.err
	}
	return net.ErrClosed
}
