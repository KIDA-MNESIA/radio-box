package group

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	M "github.com/sagernet/sing/common/metadata"
)

type fakeOutbound struct {
	tag      string
	networks []string

	dialErr   atomic.Value // *errBox
	listenErr atomic.Value // *errBox

	dialCalls   atomic.Int32
	listenCalls atomic.Int32
}

func newFakeOutbound(tag string, networks ...string) *fakeOutbound {
	o := &fakeOutbound{
		tag:      tag,
		networks: networks,
	}
	o.dialErr.Store(&errBox{})
	o.listenErr.Store(&errBox{})
	return o
}

func (o *fakeOutbound) Type() string { return "fake" }
func (o *fakeOutbound) Tag() string  { return o.tag }
func (o *fakeOutbound) Network() []string {
	return o.networks
}
func (o *fakeOutbound) Dependencies() []string { return nil }

func (o *fakeOutbound) SetDialError(err error) {
	o.dialErr.Store(&errBox{err: err})
}

func (o *fakeOutbound) SetListenError(err error) {
	o.listenErr.Store(&errBox{err: err})
}

func (o *fakeOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	o.dialCalls.Add(1)
	if err := o.dialErr.Load().(*errBox).err; err != nil {
		return nil, err
	}
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, nil
}

func (o *fakeOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	o.listenCalls.Add(1)
	if err := o.listenErr.Load().(*errBox).err; err != nil {
		return nil, err
	}
	return &fakePacketConn{}, nil
}

func (o *fakeOutbound) DialCalls() int {
	return int(o.dialCalls.Load())
}

func (o *fakeOutbound) ListenCalls() int {
	return int(o.listenCalls.Load())
}

type fakePacketConn struct {
	closed atomic.Bool
}

func (c *fakePacketConn) ReadFrom(_ []byte) (n int, addr net.Addr, err error) {
	return 0, nil, io.EOF
}

func (c *fakePacketConn) WriteTo(_ []byte, _ net.Addr) (n int, err error) {
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	return 0, errors.New("not implemented")
}

func (c *fakePacketConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *fakePacketConn) LocalAddr() net.Addr { return &net.IPAddr{} }

func (c *fakePacketConn) SetDeadline(_ time.Time) error { return nil }
func (c *fakePacketConn) SetReadDeadline(_ time.Time) error {
	return nil
}
func (c *fakePacketConn) SetWriteDeadline(_ time.Time) error {
	return nil
}

type errBox struct {
	err error
}

var _ adapter.Outbound = (*fakeOutbound)(nil)
