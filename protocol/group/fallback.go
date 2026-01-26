package group

import (
	"context"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterFallback(registry *outbound.Registry) {
	outbound.Register[option.FallbackOutboundOptions](registry, C.TypeFallback, NewFallback)
}

var (
	_ adapter.OutboundGroup             = (*Fallback)(nil)
	_ adapter.ConnectionHandlerEx       = (*Fallback)(nil)
	_ adapter.PacketConnectionHandlerEx = (*Fallback)(nil)
)

type Fallback struct {
	outbound.Adapter
	ctx                          context.Context
	outbound                     adapter.OutboundManager
	connection                   adapter.ConnectionManager
	logger                       log.ContextLogger
	tags                         []string
	link                         string
	interval                     time.Duration
	idleTimeout                  time.Duration
	timeout                      time.Duration
	group                        *FallbackGroup
	interruptExternalConnections bool
}

func NewFallback(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.FallbackOutboundOptions) (adapter.Outbound, error) {
	outbound := &Fallback{
		Adapter:                      outbound.NewAdapter(C.TypeFallback, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:                          ctx,
		outbound:                     service.FromContext[adapter.OutboundManager](ctx),
		connection:                   service.FromContext[adapter.ConnectionManager](ctx),
		logger:                       logger,
		tags:                         options.Outbounds,
		link:                         options.URL,
		interval:                     time.Duration(options.Interval),
		idleTimeout:                  time.Duration(options.IdleTimeout),
		timeout:                      time.Duration(options.Timeout),
		interruptExternalConnections: options.InterruptExistConnections,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	return outbound, nil
}

func (s *Fallback) Start() error {
	outbounds := make([]adapter.Outbound, 0, len(s.tags))
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	group, err := NewFallbackGroup(s.ctx, s.outbound, s.logger, outbounds, s.link, s.interval, s.idleTimeout, s.timeout, s.interruptExternalConnections)
	if err != nil {
		return err
	}
	s.group = group
	return nil
}

func (s *Fallback) PostStart() error {
	s.group.PostStart()
	return nil
}

func (s *Fallback) Close() error {
	return common.Close(
		common.PtrOrNil(s.group),
	)
}

func (s *Fallback) Now() string {
	return s.group.Now()
}

func (s *Fallback) All() []string {
	return s.tags
}

func (s *Fallback) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	return s.group.DialContext(ctx, network, destination)
}

func (s *Fallback) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	return s.group.ListenPacket(ctx, destination)
}

func (s *Fallback) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *Fallback) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

type FallbackGroup struct {
	checker                      *healthChecker
	logger                       log.ContextLogger
	outbounds                    []adapter.Outbound
	selectedOutboundTCP          common.TypedValue[adapter.Outbound]
	selectedOutboundUDP          common.TypedValue[adapter.Outbound]
	interruptGroup               *interrupt.Group
	interruptExternalConnections bool
}

func NewFallbackGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.ContextLogger, outbounds []adapter.Outbound, link string, interval time.Duration, idleTimeout time.Duration, timeout time.Duration, interruptExternalConnections bool) (*FallbackGroup, error) {
	group := &FallbackGroup{
		logger:                       logger,
		outbounds:                    outbounds,
		interruptGroup:               interrupt.NewGroup(),
		interruptExternalConnections: interruptExternalConnections,
	}
	checker, err := newHealthChecker(ctx, outboundManager, logger, outbounds, link, interval, idleTimeout, timeout, group.performUpdateCheck)
	if err != nil {
		return nil, err
	}
	group.checker = checker
	return group, nil
}

func (g *FallbackGroup) PostStart() {
	g.checker.PostStart()
}

func (g *FallbackGroup) Touch() {
	g.checker.Touch()
}

func (g *FallbackGroup) Close() error {
	return g.checker.Close()
}

func (g *FallbackGroup) Now() string {
	if outboundTCP := g.selectedOutboundTCP.Load(); outboundTCP != nil {
		return outboundTCP.Tag()
	}
	if outboundUDP := g.selectedOutboundUDP.Load(); outboundUDP != nil {
		return outboundUDP.Tag()
	}
	if len(g.outbounds) > 0 {
		return g.outbounds[0].Tag()
	}
	return ""
}

func (g *FallbackGroup) Select(network string) (adapter.Outbound, bool) {
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		if g.checker.history.LoadURLTestHistory(RealTag(detour)) != nil {
			return detour, true
		}
	}
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		return detour, false
	}
	return nil, false
}

func (g *FallbackGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	candidates := make([]adapter.Outbound, 0, len(g.outbounds))
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		candidates = append(candidates, detour)
	}
	if len(candidates) == 0 {
		return nil, E.New("missing supported outbound")
	}
	preferred := make([]adapter.Outbound, 0, len(candidates))
	others := make([]adapter.Outbound, 0, len(candidates))
	for _, detour := range candidates {
		if g.checker.history.LoadURLTestHistory(RealTag(detour)) != nil {
			preferred = append(preferred, detour)
		} else {
			others = append(others, detour)
		}
	}
	tryList := append(preferred, others...)
	var lastErr error
	for _, detour := range tryList {
		conn, err := detour.DialContext(ctx, network, destination)
		if err == nil {
			g.storeSelected(network, detour)
			return g.interruptGroup.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
		}
		lastErr = err
		g.logger.ErrorContext(ctx, err)
		g.checker.history.DeleteURLTestHistory(RealTag(detour))
		go g.checker.CheckOutbounds(true)
	}
	return nil, lastErr
}

func (g *FallbackGroup) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	candidates := make([]adapter.Outbound, 0, len(g.outbounds))
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), N.NetworkUDP) {
			continue
		}
		candidates = append(candidates, detour)
	}
	if len(candidates) == 0 {
		return nil, E.New("missing supported outbound")
	}
	preferred := make([]adapter.Outbound, 0, len(candidates))
	others := make([]adapter.Outbound, 0, len(candidates))
	for _, detour := range candidates {
		if g.checker.history.LoadURLTestHistory(RealTag(detour)) != nil {
			preferred = append(preferred, detour)
		} else {
			others = append(others, detour)
		}
	}
	tryList := append(preferred, others...)
	var lastErr error
	for _, detour := range tryList {
		conn, err := detour.ListenPacket(ctx, destination)
		if err == nil {
			g.storeSelected(N.NetworkUDP, detour)
			return g.interruptGroup.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
		}
		lastErr = err
		g.logger.ErrorContext(ctx, err)
		g.checker.history.DeleteURLTestHistory(RealTag(detour))
		go g.checker.CheckOutbounds(true)
	}
	return nil, lastErr
}

func (g *FallbackGroup) storeSelected(network string, outbound adapter.Outbound) {
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		previous := g.selectedOutboundTCP.Swap(outbound)
		if previous != nil && previous != outbound {
			g.interruptGroup.Interrupt(g.interruptExternalConnections)
		}
	case N.NetworkUDP:
		previous := g.selectedOutboundUDP.Swap(outbound)
		if previous != nil && previous != outbound {
			g.interruptGroup.Interrupt(g.interruptExternalConnections)
		}
	}
}

func (g *FallbackGroup) performUpdateCheck() {
	var updated bool
	if outbound, exists := g.Select(N.NetworkTCP); outbound != nil {
		previous := g.selectedOutboundTCP.Load()
		if previous == nil || (exists && outbound != previous) {
			if previous != nil && outbound != previous {
				updated = true
			}
			g.selectedOutboundTCP.Store(outbound)
		}
	}
	if outbound, exists := g.Select(N.NetworkUDP); outbound != nil {
		previous := g.selectedOutboundUDP.Load()
		if previous == nil || (exists && outbound != previous) {
			if previous != nil && outbound != previous {
				updated = true
			}
			g.selectedOutboundUDP.Store(outbound)
		}
	}
	if updated {
		g.interruptGroup.Interrupt(g.interruptExternalConnections)
	}
}
