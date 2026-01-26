package group

import (
	"context"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
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
	"golang.org/x/net/publicsuffix"
)

func RegisterLoadBalance(registry *outbound.Registry) {
	outbound.Register[option.LoadBalanceOutboundOptions](registry, C.TypeLoadBalance, NewLoadBalance)
}

var (
	_ adapter.OutboundGroup             = (*LoadBalance)(nil)
	_ adapter.ConnectionHandlerEx       = (*LoadBalance)(nil)
	_ adapter.PacketConnectionHandlerEx = (*LoadBalance)(nil)
)

type LoadBalance struct {
	outbound.Adapter
	ctx        context.Context
	outbound   adapter.OutboundManager
	connection adapter.ConnectionManager
	logger     log.ContextLogger

	tags        []string
	link        string
	interval    time.Duration
	idleTimeout time.Duration
	timeout     time.Duration
	strategy    option.LoadBalanceStrategy

	group *LoadBalanceGroup
}

func NewLoadBalance(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.LoadBalanceOutboundOptions) (adapter.Outbound, error) {
	strategy := options.Strategy
	if strategy == "" {
		strategy = option.LoadBalanceStrategyRoundRobin
	}
	outbound := &LoadBalance{
		Adapter:     outbound.NewAdapter(C.TypeLoadBalance, tag, []string{N.NetworkTCP, N.NetworkUDP}, options.Outbounds),
		ctx:         ctx,
		outbound:    service.FromContext[adapter.OutboundManager](ctx),
		connection:  service.FromContext[adapter.ConnectionManager](ctx),
		logger:      logger,
		tags:        options.Outbounds,
		link:        options.URL,
		interval:    time.Duration(options.Interval),
		idleTimeout: time.Duration(options.IdleTimeout),
		timeout:     time.Duration(options.Timeout),
		strategy:    strategy,
	}
	if len(outbound.tags) == 0 {
		return nil, E.New("missing tags")
	}
	switch strategy {
	case option.LoadBalanceStrategyRoundRobin, option.LoadBalanceStrategyConsistentHashing, option.LoadBalanceStrategyStickySessions:
	default:
		return nil, E.New("unknown load-balance strategy: ", strategy)
	}
	return outbound, nil
}

func (s *LoadBalance) Start() error {
	outbounds := make([]adapter.Outbound, 0, len(s.tags))
	for i, tag := range s.tags {
		detour, loaded := s.outbound.Outbound(tag)
		if !loaded {
			return E.New("outbound ", i, " not found: ", tag)
		}
		outbounds = append(outbounds, detour)
	}
	group, err := NewLoadBalanceGroup(s.ctx, s.outbound, s.logger, outbounds, s.link, s.interval, s.idleTimeout, s.timeout, s.strategy)
	if err != nil {
		return err
	}
	s.group = group
	return nil
}

func (s *LoadBalance) PostStart() error {
	s.group.PostStart()
	return nil
}

func (s *LoadBalance) Close() error {
	return common.Close(
		common.PtrOrNil(s.group),
	)
}

func (s *LoadBalance) Now() string {
	return s.group.Now()
}

func (s *LoadBalance) All() []string {
	return s.tags
}

func (s *LoadBalance) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	s.group.Touch()
	return s.group.DialContext(ctx, network, destination)
}

func (s *LoadBalance) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	s.group.Touch()
	return s.group.ListenPacket(ctx, destination)
}

func (s *LoadBalance) NewConnectionEx(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewConnection(ctx, s, conn, metadata, onClose)
}

func (s *LoadBalance) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	ctx = interrupt.ContextWithIsExternalConnection(ctx)
	s.connection.NewPacketConnection(ctx, s, conn, metadata, onClose)
}

type stickyEntry struct {
	tag    string
	expire time.Time
}

type LoadBalanceGroup struct {
	checker     *healthChecker
	logger      log.ContextLogger
	outbounds   []adapter.Outbound
	outboundMap map[string]adapter.Outbound
	strategy    option.LoadBalanceStrategy

	rrCounter atomic.Uint64

	stickyAccess sync.Mutex
	stickyCache  map[string]stickyEntry

	lastSelected common.TypedValue[adapter.Outbound]
}

func NewLoadBalanceGroup(ctx context.Context, outboundManager adapter.OutboundManager, logger log.ContextLogger, outbounds []adapter.Outbound, link string, interval time.Duration, idleTimeout time.Duration, timeout time.Duration, strategy option.LoadBalanceStrategy) (*LoadBalanceGroup, error) {
	group := &LoadBalanceGroup{
		logger:      logger,
		outbounds:   outbounds,
		outboundMap: make(map[string]adapter.Outbound),
		strategy:    strategy,
		stickyCache: make(map[string]stickyEntry),
	}
	for _, detour := range outbounds {
		group.outboundMap[detour.Tag()] = detour
	}
	checker, err := newHealthChecker(ctx, outboundManager, logger, outbounds, link, interval, idleTimeout, timeout, nil)
	if err != nil {
		return nil, err
	}
	group.checker = checker
	return group, nil
}

func (g *LoadBalanceGroup) PostStart() {
	g.checker.PostStart()
}

func (g *LoadBalanceGroup) Touch() {
	g.checker.Touch()
}

func (g *LoadBalanceGroup) Close() error {
	return g.checker.Close()
}

func (g *LoadBalanceGroup) Now() string {
	if selected := g.lastSelected.Load(); selected != nil {
		return selected.Tag()
	}
	if len(g.outbounds) > 0 {
		return g.outbounds[0].Tag()
	}
	return ""
}

func (g *LoadBalanceGroup) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	excluded := make(map[adapter.Outbound]bool)
	var lastErr error
	triggeredCheck := false
	for {
		candidates := g.candidates(network)
		candidates = common.Filter(candidates, func(it adapter.Outbound) bool { return !excluded[it] })
		if len(candidates) == 0 {
			if lastErr == nil {
				lastErr = E.New("missing supported outbound")
			}
			return nil, lastErr
		}
		detour, err := g.selectOutbound(ctx, network, destination, candidates)
		if err != nil {
			return nil, err
		}
		conn, err := detour.DialContext(ctx, network, destination)
		if err == nil {
			g.lastSelected.Store(detour)
			if g.strategy == option.LoadBalanceStrategyStickySessions {
				g.storeSticky(ctx, network, destination, detour)
			}
			return conn, nil
		}
		lastErr = err
		g.logger.ErrorContext(ctx, err)
		g.checker.history.DeleteURLTestHistory(RealTag(detour))
		if g.strategy == option.LoadBalanceStrategyStickySessions {
			g.deleteSticky(ctx, network, destination)
		}
		excluded[detour] = true
		if !triggeredCheck {
			triggeredCheck = true
			go g.checker.CheckOutbounds(true)
		}
	}
}

func (g *LoadBalanceGroup) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	excluded := make(map[adapter.Outbound]bool)
	var lastErr error
	triggeredCheck := false
	for {
		candidates := g.candidates(N.NetworkUDP)
		candidates = common.Filter(candidates, func(it adapter.Outbound) bool { return !excluded[it] })
		if len(candidates) == 0 {
			if lastErr == nil {
				lastErr = E.New("missing supported outbound")
			}
			return nil, lastErr
		}
		detour, err := g.selectOutbound(ctx, N.NetworkUDP, destination, candidates)
		if err != nil {
			return nil, err
		}
		conn, err := detour.ListenPacket(ctx, destination)
		if err == nil {
			g.lastSelected.Store(detour)
			if g.strategy == option.LoadBalanceStrategyStickySessions {
				g.storeSticky(ctx, N.NetworkUDP, destination, detour)
			}
			return conn, nil
		}
		lastErr = err
		g.logger.ErrorContext(ctx, err)
		g.checker.history.DeleteURLTestHistory(RealTag(detour))
		if g.strategy == option.LoadBalanceStrategyStickySessions {
			g.deleteSticky(ctx, N.NetworkUDP, destination)
		}
		excluded[detour] = true
		if !triggeredCheck {
			triggeredCheck = true
			go g.checker.CheckOutbounds(true)
		}
	}
}

func (g *LoadBalanceGroup) candidates(network string) []adapter.Outbound {
	networkCandidates := make([]adapter.Outbound, 0, len(g.outbounds))
	available := make([]adapter.Outbound, 0, len(g.outbounds))
	for _, detour := range g.outbounds {
		if !common.Contains(detour.Network(), network) {
			continue
		}
		networkCandidates = append(networkCandidates, detour)
		if g.checker.history.LoadURLTestHistory(RealTag(detour)) != nil {
			available = append(available, detour)
		}
	}
	if len(available) > 0 {
		return available
	}
	return networkCandidates
}

func (g *LoadBalanceGroup) selectOutbound(ctx context.Context, network string, destination M.Socksaddr, candidates []adapter.Outbound) (adapter.Outbound, error) {
	switch g.strategy {
	case option.LoadBalanceStrategyRoundRobin:
		return g.selectRoundRobin(candidates), nil
	case option.LoadBalanceStrategyConsistentHashing:
		return g.selectConsistentHashing(destination, candidates), nil
	case option.LoadBalanceStrategyStickySessions:
		if cached := g.loadSticky(ctx, network, destination, candidates); cached != nil {
			return cached, nil
		}
		return g.selectSticky(ctx, network, destination, candidates), nil
	default:
		return nil, E.New("unknown load-balance strategy: ", g.strategy)
	}
}

func (g *LoadBalanceGroup) selectRoundRobin(candidates []adapter.Outbound) adapter.Outbound {
	index := (g.rrCounter.Add(1) - 1) % uint64(len(candidates))
	return candidates[index]
}

func (g *LoadBalanceGroup) selectConsistentHashing(destination M.Socksaddr, candidates []adapter.Outbound) adapter.Outbound {
	key := destinationKey(destination)
	return rendezvousPick(key, candidates)
}

func (g *LoadBalanceGroup) selectSticky(ctx context.Context, network string, destination M.Socksaddr, candidates []adapter.Outbound) adapter.Outbound {
	key := stickyKey(ctx, network, destination)
	return rendezvousPick(key, candidates)
}

func (g *LoadBalanceGroup) loadSticky(ctx context.Context, network string, destination M.Socksaddr, candidates []adapter.Outbound) adapter.Outbound {
	key := stickyKey(ctx, network, destination)
	now := time.Now()
	g.stickyAccess.Lock()
	entry, loaded := g.stickyCache[key]
	if loaded && now.After(entry.expire) {
		delete(g.stickyCache, key)
		loaded = false
	}
	g.stickyAccess.Unlock()
	if !loaded {
		return nil
	}
	detour := g.outboundMap[entry.tag]
	if detour == nil {
		// Fallback to reselect if the outbound was removed or does not match the requested network.
		g.stickyAccess.Lock()
		delete(g.stickyCache, key)
		g.stickyAccess.Unlock()
		return nil
	}
	for _, candidate := range candidates {
		if candidate == detour {
			return detour
		}
	}
	return nil
}

func (g *LoadBalanceGroup) storeSticky(ctx context.Context, network string, destination M.Socksaddr, detour adapter.Outbound) {
	key := stickyKey(ctx, network, destination)
	g.stickyAccess.Lock()
	g.stickyCache[key] = stickyEntry{
		tag:    detour.Tag(),
		expire: time.Now().Add(10 * time.Minute),
	}
	g.stickyAccess.Unlock()
}

func (g *LoadBalanceGroup) deleteSticky(ctx context.Context, network string, destination M.Socksaddr) {
	key := stickyKey(ctx, network, destination)
	g.stickyAccess.Lock()
	delete(g.stickyCache, key)
	g.stickyAccess.Unlock()
}

func stickyKey(ctx context.Context, network string, destination M.Socksaddr) string {
	sourceKey := ""
	if inboundCtx := adapter.ContextFrom(ctx); inboundCtx != nil && inboundCtx.Source.IsValid() {
		sourceKey = inboundCtx.Source.AddrString()
	}
	return network + "|" + sourceKey + "|" + destinationKey(destination)
}

func destinationKey(destination M.Socksaddr) string {
	host := destination.AddrString()
	if destination.IsFqdn() {
		if base, err := publicsuffix.EffectiveTLDPlusOne(destination.Fqdn); err == nil {
			host = base
		} else {
			host = destination.Fqdn
		}
	}
	return host + ":" + strconv.Itoa(int(destination.Port))
}

func rendezvousPick(key string, candidates []adapter.Outbound) adapter.Outbound {
	var selected adapter.Outbound
	var bestScore uint64
	for _, detour := range candidates {
		score := fnv1a64(key + "|" + detour.Tag())
		if selected == nil || score > bestScore {
			selected = detour
			bestScore = score
		}
	}
	return selected
}

func fnv1a64(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	var hash uint64 = offset64
	for i := 0; i < len(s); i++ {
		hash ^= uint64(s[i])
		hash *= prime64
	}
	return hash
}
