package dns

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/taskmonitor"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/libbox/platform"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	F "github.com/sagernet/sing/common/format"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/contrab/freelru"
	"github.com/sagernet/sing/contrab/maphash"
	"github.com/sagernet/sing/service"

	mDNS "github.com/miekg/dns"
)

var _ adapter.DNSRouter = (*Router)(nil)

type Router struct {
	ctx                   context.Context
	logger                logger.ContextLogger
	transport             adapter.DNSTransportManager
	outbound              adapter.OutboundManager
	client                adapter.DNSClient
	rules                 []adapter.DNSRule
	defaultDomainStrategy C.DomainStrategy
	upstreamTimeout       time.Duration
	fallbackTimeout       time.Duration
	fallbackGrace         time.Duration
	dnsReverseMapping     freelru.Cache[netip.Addr, string]
	platformInterface     platform.Interface
}

func NewRouter(ctx context.Context, logFactory log.Factory, options option.DNSOptions) *Router {
	router := &Router{
		ctx:                   ctx,
		logger:                logFactory.NewLogger("dns"),
		transport:             service.FromContext[adapter.DNSTransportManager](ctx),
		outbound:              service.FromContext[adapter.OutboundManager](ctx),
		rules:                 make([]adapter.DNSRule, 0, len(options.Rules)),
		defaultDomainStrategy: C.DomainStrategy(options.Strategy),
	}
	router.upstreamTimeout = time.Duration(options.DNSClientOptions.UpstreamTimeoutMS) * time.Millisecond
	router.fallbackTimeout = time.Duration(options.DNSClientOptions.FallbackTimeoutMS) * time.Millisecond
	router.fallbackGrace = time.Duration(options.DNSClientOptions.FallbackGraceMS) * time.Millisecond
	router.client = NewClient(ClientOptions{
		DisableCache:     options.DNSClientOptions.DisableCache,
		DisableExpire:    options.DNSClientOptions.DisableExpire,
		IndependentCache: options.DNSClientOptions.IndependentCache,
		CacheCapacity:    options.DNSClientOptions.CacheCapacity,
		ClientSubnet:     options.DNSClientOptions.ClientSubnet.Build(netip.Prefix{}),
		RDRC: func() adapter.RDRCStore {
			cacheFile := service.FromContext[adapter.CacheFile](ctx)
			if cacheFile == nil {
				return nil
			}
			if !cacheFile.StoreRDRC() {
				return nil
			}
			return cacheFile
		},
		Logger: router.logger,
	})
	if options.ReverseMapping {
		router.dnsReverseMapping = common.Must1(freelru.NewSharded[netip.Addr, string](1024, maphash.NewHasher[netip.Addr]().Hash32))
	}
	return router
}

func (r *Router) Initialize(rules []option.DNSRule) error {
	for i, ruleOptions := range rules {
		dnsRule, err := R.NewDNSRule(r.ctx, r.logger, ruleOptions, true)
		if err != nil {
			return E.Cause(err, "parse dns rule[", i, "]")
		}
		r.rules = append(r.rules, dnsRule)
	}
	return nil
}

func (r *Router) Start(stage adapter.StartStage) error {
	monitor := taskmonitor.New(r.logger, C.StartTimeout)
	switch stage {
	case adapter.StartStateStart:
		monitor.Start("initialize DNS client")
		r.client.Start()
		monitor.Finish()

		for i, rule := range r.rules {
			monitor.Start("initialize DNS rule[", i, "]")
			err := rule.Start()
			monitor.Finish()
			if err != nil {
				return E.Cause(err, "initialize DNS rule[", i, "]")
			}
		}
	}
	return nil
}

func (r *Router) Close() error {
	monitor := taskmonitor.New(r.logger, C.StopTimeout)
	var err error
	for i, rule := range r.rules {
		monitor.Start("close dns rule[", i, "]")
		err = E.Append(err, rule.Close(), func(err error) error {
			return E.Cause(err, "close dns rule[", i, "]")
		})
		monitor.Finish()
	}
	return err
}

func (r *Router) matchDNS(ctx context.Context, allowFakeIP bool, ruleIndex int, isAddressQuery bool, options *adapter.DNSQueryOptions) ([]adapter.DNSTransport, []adapter.DNSTransport, time.Duration, time.Duration, time.Duration, adapter.DNSRule, int) {
	metadata := adapter.ContextFrom(ctx)
	if metadata == nil {
		panic("no context")
	}
	var currentRuleIndex int
	if ruleIndex != -1 {
		currentRuleIndex = ruleIndex + 1
	}
	for ; currentRuleIndex < len(r.rules); currentRuleIndex++ {
		currentRule := r.rules[currentRuleIndex]
		if currentRule.WithAddressLimit() && !isAddressQuery {
			continue
		}
		metadata.ResetRuleCache()
		if currentRule.Match(metadata) {
			displayRuleIndex := currentRuleIndex
			if displayRuleIndex != -1 {
				displayRuleIndex += displayRuleIndex + 1
			}
			ruleDescription := currentRule.String()
			if ruleDescription != "" {
				r.logger.DebugContext(ctx, "match[", displayRuleIndex, "] ", currentRule, " => ", currentRule.Action())
			} else {
				r.logger.DebugContext(ctx, "match[", displayRuleIndex, "] => ", currentRule.Action())
			}
			switch action := currentRule.Action().(type) {
			case *R.RuleActionDNSRoute:
				if len(action.Servers) == 0 {
					continue
				}
				serverSet := make(map[string]struct{}, len(action.Servers))
				transports := make([]adapter.DNSTransport, 0, len(action.Servers))
				var hasFakeIP bool
				for _, serverTag := range action.Servers {
					if serverTag == "" {
						continue
					}
					if _, ok := serverSet[serverTag]; ok {
						continue
					}
					serverSet[serverTag] = struct{}{}
					transport, loaded := r.transport.Transport(serverTag)
					if !loaded {
						r.logger.ErrorContext(ctx, "transport not found: ", serverTag)
						continue
					}
					isFakeIP := transport.Type() == C.DNSTypeFakeIP
					if isFakeIP {
						hasFakeIP = true
						if !allowFakeIP {
							continue
						}
					}
					transports = append(transports, transport)
				}
				if len(transports) == 0 {
					continue
				}
				fallbackSet := make(map[string]struct{}, len(action.FallbackServers))
				fallbackTransports := make([]adapter.DNSTransport, 0, len(action.FallbackServers))
				for _, serverTag := range action.FallbackServers {
					if serverTag == "" {
						continue
					}
					if _, ok := fallbackSet[serverTag]; ok {
						continue
					}
					fallbackSet[serverTag] = struct{}{}
					transport, loaded := r.transport.Transport(serverTag)
					if !loaded {
						r.logger.ErrorContext(ctx, "fallback transport not found: ", serverTag)
						continue
					}
					isFakeIP := transport.Type() == C.DNSTypeFakeIP
					if isFakeIP {
						hasFakeIP = true
						if !allowFakeIP {
							continue
						}
					}
					fallbackTransports = append(fallbackTransports, transport)
				}
				if action.Strategy != C.DomainStrategyAsIS {
					options.Strategy = action.Strategy
				}
				if hasFakeIP || action.DisableCache {
					options.DisableCache = true
				}
				if action.RewriteTTL != nil {
					options.RewriteTTL = action.RewriteTTL
				}
				if action.ClientSubnet.IsValid() {
					options.ClientSubnet = action.ClientSubnet
				}
				if len(transports) == 1 {
					if legacyTransport, isLegacy := transports[0].(adapter.LegacyDNSTransport); isLegacy {
						if options.Strategy == C.DomainStrategyAsIS {
							options.Strategy = legacyTransport.LegacyStrategy()
						}
						if !options.ClientSubnet.IsValid() {
							options.ClientSubnet = legacyTransport.LegacyClientSubnet()
						}
					}
				}
				upstreamTimeout := action.UpstreamTimeout
				if upstreamTimeout == 0 {
					upstreamTimeout = r.upstreamTimeout
				}
				fallbackTimeout := action.FallbackTimeout
				if fallbackTimeout == 0 {
					fallbackTimeout = r.fallbackTimeout
				}
				if fallbackTimeout == 0 {
					fallbackTimeout = upstreamTimeout
				}
				fallbackGrace := action.FallbackGrace
				if fallbackGrace == 0 {
					fallbackGrace = r.fallbackGrace
				}
				return transports, fallbackTransports, upstreamTimeout, fallbackTimeout, fallbackGrace, currentRule, currentRuleIndex
			case *R.RuleActionDNSRouteOptions:
				if action.Strategy != C.DomainStrategyAsIS {
					options.Strategy = action.Strategy
				}
				if action.DisableCache {
					options.DisableCache = true
				}
				if action.RewriteTTL != nil {
					options.RewriteTTL = action.RewriteTTL
				}
				if action.ClientSubnet.IsValid() {
					options.ClientSubnet = action.ClientSubnet
				}
			case *R.RuleActionReject:
				return nil, nil, r.upstreamTimeout, r.fallbackTimeout, r.fallbackGrace, currentRule, currentRuleIndex
			case *R.RuleActionPredefined:
				return nil, nil, r.upstreamTimeout, r.fallbackTimeout, r.fallbackGrace, currentRule, currentRuleIndex
			}
		}
	}
	defaultTransport := r.transport.Default()
	if defaultTransport == nil {
		return nil, nil, r.upstreamTimeout, r.fallbackTimeout, r.fallbackGrace, nil, -1
	}
	return []adapter.DNSTransport{defaultTransport}, nil, r.upstreamTimeout, r.fallbackTimeout, r.fallbackGrace, nil, -1
}

func (r *Router) Exchange(ctx context.Context, message *mDNS.Msg, options adapter.DNSQueryOptions) (*mDNS.Msg, error) {
	if len(message.Question) != 1 {
		r.logger.WarnContext(ctx, "bad question size: ", len(message.Question))
		responseMessage := mDNS.Msg{
			MsgHdr: mDNS.MsgHdr{
				Id:       message.Id,
				Response: true,
				Rcode:    mDNS.RcodeFormatError,
			},
			Question: message.Question,
		}
		return &responseMessage, nil
	}
	r.logger.DebugContext(ctx, "exchange ", FormatQuestion(message.Question[0].String()))
	var (
		response          *mDNS.Msg
		selectedTransport adapter.DNSTransport
		err              error
	)
	var metadata *adapter.InboundContext
	ctx, metadata = adapter.ExtendContext(ctx)
	metadata.Destination = M.Socksaddr{}
	metadata.QueryType = message.Question[0].Qtype
	switch metadata.QueryType {
	case mDNS.TypeA:
		metadata.IPVersion = 4
	case mDNS.TypeAAAA:
		metadata.IPVersion = 6
	}
	metadata.Domain = FqdnToDomain(message.Question[0].Name)
	if options.Transport != nil {
		selectedTransport = options.Transport
		if legacyTransport, isLegacy := selectedTransport.(adapter.LegacyDNSTransport); isLegacy {
			if options.Strategy == C.DomainStrategyAsIS {
				options.Strategy = legacyTransport.LegacyStrategy()
			}
			if !options.ClientSubnet.IsValid() {
				options.ClientSubnet = legacyTransport.LegacyClientSubnet()
			}
		}
		if options.Strategy == C.DomainStrategyAsIS {
			options.Strategy = r.defaultDomainStrategy
		}
		queryCtx := ctx
		var cancel context.CancelFunc
		if r.upstreamTimeout > 0 {
			queryCtx, cancel = context.WithTimeout(ctx, r.upstreamTimeout)
		}
		response, err = r.client.Exchange(queryCtx, selectedTransport, message, options, nil)
		if cancel != nil {
			cancel()
		}
	} else {
		var (
			rule       adapter.DNSRule
			ruleIndex  int
			transports []adapter.DNSTransport
		)
		ruleIndex = -1
		for {
			dnsCtx := adapter.OverrideContext(ctx)
			dnsOptions := options
			var (
				fallbackTransports []adapter.DNSTransport
				upstreamTimeout    time.Duration
				fallbackTimeout    time.Duration
				fallbackGrace      time.Duration
			)
			transports, fallbackTransports, upstreamTimeout, fallbackTimeout, fallbackGrace, rule, ruleIndex = r.matchDNS(ctx, true, ruleIndex, isAddressQuery(message), &dnsOptions)
			if rule != nil {
				switch action := rule.Action().(type) {
				case *R.RuleActionReject:
					switch action.Method {
					case C.RuleActionRejectMethodDefault:
						return &mDNS.Msg{
							MsgHdr: mDNS.MsgHdr{
								Id:       message.Id,
								Rcode:    mDNS.RcodeRefused,
								Response: true,
							},
							Question: []mDNS.Question{message.Question[0]},
						}, nil
					case C.RuleActionRejectMethodDrop:
						return nil, tun.ErrDrop
					}
				case *R.RuleActionPredefined:
					return action.Response(message), nil
				}
			}
			withAddressLimit := rule != nil && rule.WithAddressLimit()
			primaryOptions := dnsOptions
			fallbackOptions := dnsOptions
			if len(transports) == 1 {
				if legacyTransport, isLegacy := transports[0].(adapter.LegacyDNSTransport); isLegacy {
					if primaryOptions.Strategy == C.DomainStrategyAsIS {
						primaryOptions.Strategy = legacyTransport.LegacyStrategy()
					}
					if !primaryOptions.ClientSubnet.IsValid() {
						primaryOptions.ClientSubnet = legacyTransport.LegacyClientSubnet()
					}
				}
			}
			if len(fallbackTransports) == 1 {
				if legacyTransport, isLegacy := fallbackTransports[0].(adapter.LegacyDNSTransport); isLegacy {
					if fallbackOptions.Strategy == C.DomainStrategyAsIS {
						fallbackOptions.Strategy = legacyTransport.LegacyStrategy()
					}
					if !fallbackOptions.ClientSubnet.IsValid() {
						fallbackOptions.ClientSubnet = legacyTransport.LegacyClientSubnet()
					}
				}
			}
			if primaryOptions.Strategy == C.DomainStrategyAsIS {
				primaryOptions.Strategy = r.defaultDomainStrategy
			}
			if fallbackOptions.Strategy == C.DomainStrategyAsIS {
				fallbackOptions.Strategy = r.defaultDomainStrategy
			}
			if client, ok := r.client.(*Client); ok && !client.independentCache {
				if len(transports) > 1 {
					// Avoid global cache pollution and cacheLock serialisation when racing.
					primaryOptions.DisableCache = true
				}
				if upstreamTimeout > 0 && len(fallbackTransports) > 0 {
					// Avoid cacheLock serialisation/pollution when starting fallback queries.
					fallbackOptions.DisableCache = true
				}
			}
			if upstreamTimeout > 0 && len(fallbackTransports) > 0 {
				response, selectedTransport, err = r.exchangeHedgedRacer(dnsCtx, transports, fallbackTransports, message, primaryOptions, fallbackOptions, rule, withAddressLimit, upstreamTimeout, fallbackTimeout, fallbackGrace)
			} else if upstreamTimeout > 0 {
				queryCtx, cancel := context.WithTimeout(dnsCtx, upstreamTimeout)
				response, selectedTransport, err = r.exchangeRacer(queryCtx, transports, message, primaryOptions, rule, withAddressLimit)
				cancel()
			} else {
				response, selectedTransport, err = r.exchangeRacer(dnsCtx, transports, message, primaryOptions, rule, withAddressLimit)
			}
			var rejected bool
			if err != nil {
				if errors.Is(err, ErrResponseRejectedCached) {
					rejected = true
					r.logger.DebugContext(ctx, E.Cause(err, "response rejected for ", FormatQuestion(message.Question[0].String())), " (cached)")
				} else if errors.Is(err, ErrResponseRejected) {
					rejected = true
					r.logger.DebugContext(ctx, E.Cause(err, "response rejected for ", FormatQuestion(message.Question[0].String())))
				} else if len(message.Question) > 0 {
					r.logger.ErrorContext(ctx, E.Cause(err, "exchange failed for ", FormatQuestion(message.Question[0].String())))
				} else {
					r.logger.ErrorContext(ctx, E.Cause(err, "exchange failed for <empty query>"))
				}
			}
			if withAddressLimit && rejected {
				continue
			}
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if r.dnsReverseMapping != nil && len(message.Question) > 0 && response != nil && len(response.Answer) > 0 {
		if selectedTransport == nil || selectedTransport.Type() != C.DNSTypeFakeIP {
			for _, answer := range response.Answer {
				switch record := answer.(type) {
				case *mDNS.A:
					r.dnsReverseMapping.AddWithLifetime(M.AddrFromIP(record.A), FqdnToDomain(record.Hdr.Name), time.Duration(record.Hdr.Ttl)*time.Second)
				case *mDNS.AAAA:
					r.dnsReverseMapping.AddWithLifetime(M.AddrFromIP(record.AAAA), FqdnToDomain(record.Hdr.Name), time.Duration(record.Hdr.Ttl)*time.Second)
				}
			}
		}
	}
	return response, nil
}

func (r *Router) Lookup(ctx context.Context, domain string, options adapter.DNSQueryOptions) ([]netip.Addr, error) {
	var (
		responseAddrs []netip.Addr
		err           error
	)
	printResult := func() {
		if err == nil && len(responseAddrs) == 0 {
			err = E.New("empty result")
		}
		if err != nil {
			if errors.Is(err, ErrResponseRejectedCached) {
				r.logger.DebugContext(ctx, "response rejected for ", domain, " (cached)")
			} else if errors.Is(err, ErrResponseRejected) {
				r.logger.DebugContext(ctx, "response rejected for ", domain)
			} else {
				r.logger.ErrorContext(ctx, E.Cause(err, "lookup failed for ", domain))
			}
		}
		if err != nil {
			err = E.Cause(err, "lookup ", domain)
		}
	}
	r.logger.DebugContext(ctx, "lookup domain ", domain)
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Destination = M.Socksaddr{}
	metadata.Domain = FqdnToDomain(domain)
	if options.Transport != nil {
		transport := options.Transport
		if legacyTransport, isLegacy := transport.(adapter.LegacyDNSTransport); isLegacy {
			if options.Strategy == C.DomainStrategyAsIS {
				options.Strategy = legacyTransport.LegacyStrategy()
			}
			if !options.ClientSubnet.IsValid() {
				options.ClientSubnet = legacyTransport.LegacyClientSubnet()
			}
		}
		if options.Strategy == C.DomainStrategyAsIS {
			options.Strategy = r.defaultDomainStrategy
		}
		queryCtx := ctx
		var cancel context.CancelFunc
		if r.upstreamTimeout > 0 {
			queryCtx, cancel = context.WithTimeout(ctx, r.upstreamTimeout)
		}
		responseAddrs, err = r.client.Lookup(queryCtx, transport, domain, options, nil)
		if cancel != nil {
			cancel()
		}
	} else {
		var (
			rule       adapter.DNSRule
			ruleIndex  int
			transports []adapter.DNSTransport
		)
		ruleIndex = -1
		for {
			dnsCtx := adapter.OverrideContext(ctx)
			dnsOptions := options
			var (
				fallbackTransports []adapter.DNSTransport
				upstreamTimeout    time.Duration
				fallbackTimeout    time.Duration
				fallbackGrace      time.Duration
			)
			transports, fallbackTransports, upstreamTimeout, fallbackTimeout, fallbackGrace, rule, ruleIndex = r.matchDNS(ctx, false, ruleIndex, true, &dnsOptions)
			if rule != nil {
				switch action := rule.Action().(type) {
				case *R.RuleActionReject:
					return nil, &R.RejectedError{Cause: action.Error(ctx)}
				case *R.RuleActionPredefined:
					if action.Rcode != mDNS.RcodeSuccess {
						err = RcodeError(action.Rcode)
					} else {
						for _, answer := range action.Answer {
							switch record := answer.(type) {
							case *mDNS.A:
								responseAddrs = append(responseAddrs, M.AddrFromIP(record.A))
							case *mDNS.AAAA:
								responseAddrs = append(responseAddrs, M.AddrFromIP(record.AAAA))
							}
						}
					}
					goto response
				}
			}
			withAddressLimit := rule != nil && rule.WithAddressLimit()
			primaryOptions := dnsOptions
			fallbackOptions := dnsOptions
			if len(transports) == 1 {
				if legacyTransport, isLegacy := transports[0].(adapter.LegacyDNSTransport); isLegacy {
					if primaryOptions.Strategy == C.DomainStrategyAsIS {
						primaryOptions.Strategy = legacyTransport.LegacyStrategy()
					}
					if !primaryOptions.ClientSubnet.IsValid() {
						primaryOptions.ClientSubnet = legacyTransport.LegacyClientSubnet()
					}
				}
			}
			if len(fallbackTransports) == 1 {
				if legacyTransport, isLegacy := fallbackTransports[0].(adapter.LegacyDNSTransport); isLegacy {
					if fallbackOptions.Strategy == C.DomainStrategyAsIS {
						fallbackOptions.Strategy = legacyTransport.LegacyStrategy()
					}
					if !fallbackOptions.ClientSubnet.IsValid() {
						fallbackOptions.ClientSubnet = legacyTransport.LegacyClientSubnet()
					}
				}
			}
			if primaryOptions.Strategy == C.DomainStrategyAsIS {
				primaryOptions.Strategy = r.defaultDomainStrategy
			}
			if fallbackOptions.Strategy == C.DomainStrategyAsIS {
				fallbackOptions.Strategy = r.defaultDomainStrategy
			}
			if client, ok := r.client.(*Client); ok && !client.independentCache {
				if len(transports) > 1 {
					// Avoid global cache pollution and cacheLock serialisation when racing.
					primaryOptions.DisableCache = true
				}
				if upstreamTimeout > 0 && len(fallbackTransports) > 0 {
					// Avoid cacheLock serialisation/pollution when starting fallback queries.
					fallbackOptions.DisableCache = true
				}
			}
			if upstreamTimeout > 0 && len(fallbackTransports) > 0 {
				responseAddrs, err = r.lookupHedgedRacer(dnsCtx, transports, fallbackTransports, domain, primaryOptions, fallbackOptions, rule, withAddressLimit, upstreamTimeout, fallbackTimeout, fallbackGrace)
			} else if upstreamTimeout > 0 {
				queryCtx, cancel := context.WithTimeout(dnsCtx, upstreamTimeout)
				responseAddrs, err = r.lookupRacer(queryCtx, transports, domain, primaryOptions, rule, withAddressLimit)
				cancel()
			} else {
				responseAddrs, err = r.lookupRacer(dnsCtx, transports, domain, primaryOptions, rule, withAddressLimit)
			}
			if !withAddressLimit || err == nil {
				break
			}
			printResult()
		}
	}
response:
	printResult()
	if len(responseAddrs) > 0 {
		r.logger.InfoContext(ctx, "lookup succeed for ", domain, ": ", strings.Join(F.MapToString(responseAddrs), " "))
	}
	return responseAddrs, err
}

func (r *Router) exchangeHedgedRacer(ctx context.Context, primaryTransports []adapter.DNSTransport, fallbackTransports []adapter.DNSTransport, message *mDNS.Msg, primaryOptions adapter.DNSQueryOptions, fallbackOptions adapter.DNSQueryOptions, rule adapter.DNSRule, withAddressLimit bool, upstreamTimeout time.Duration, fallbackTimeout time.Duration, fallbackGrace time.Duration) (*mDNS.Msg, adapter.DNSTransport, error) {
	if upstreamTimeout <= 0 || len(fallbackTransports) == 0 {
		return r.exchangeRacer(ctx, primaryTransports, message, primaryOptions, rule, withAddressLimit)
	}
	returned := make(chan struct{})
	defer close(returned)
	type queryResult struct {
		response  *mDNS.Msg
		err       error
		transport adapter.DNSTransport
	}
	results := make(chan queryResult)
	queryCtx, queryCancel := context.WithCancel(ctx)
	defer queryCancel()
	primaryTimeout := upstreamTimeout
	if fallbackGrace > 0 {
		primaryTimeout += fallbackGrace
	}
	primaryCtx, primaryCancel := context.WithTimeout(queryCtx, primaryTimeout)
	defer primaryCancel()
	fallbackStart := make(chan struct{})
	go func() {
		timer := time.NewTimer(upstreamTimeout)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-queryCtx.Done():
		}
		close(fallbackStart)
	}()
	for _, transport := range primaryTransports {
		transport := transport
		go func() {
			perQueryCtx := adapter.OverrideContext(primaryCtx)
			msgCopy := message.Copy()
			var responseCheck func(responseAddrs []netip.Addr) bool
			if withAddressLimit && rule != nil {
				metadata := adapter.ContextFrom(perQueryCtx)
				if metadata != nil {
					baseMetadata := *metadata
					responseCheck = func(responseAddrs []netip.Addr) bool {
						md := baseMetadata
						md.ResetRuleCache()
						md.DestinationAddresses = responseAddrs
						return rule.MatchAddressLimit(&md)
					}
				}
			}
			response, err := r.client.Exchange(perQueryCtx, transport, msgCopy, primaryOptions, responseCheck)
			select {
			case results <- queryResult{response, err, transport}:
			case <-returned:
			}
		}()
	}
	for _, transport := range fallbackTransports {
		transport := transport
		go func() {
			select {
			case <-fallbackStart:
			case <-queryCtx.Done():
				return
			}
			if queryCtx.Err() != nil {
				return
			}
			perQueryCtx := adapter.OverrideContext(queryCtx)
			var cancel context.CancelFunc
			if fallbackTimeout > 0 {
				perQueryCtx, cancel = context.WithTimeout(perQueryCtx, fallbackTimeout)
			}
			msgCopy := message.Copy()
			var responseCheck func(responseAddrs []netip.Addr) bool
			if withAddressLimit && rule != nil {
				metadata := adapter.ContextFrom(perQueryCtx)
				if metadata != nil {
					baseMetadata := *metadata
					responseCheck = func(responseAddrs []netip.Addr) bool {
						md := baseMetadata
						md.ResetRuleCache()
						md.DestinationAddresses = responseAddrs
						return rule.MatchAddressLimit(&md)
					}
				}
			}
			response, err := r.client.Exchange(perQueryCtx, transport, msgCopy, fallbackOptions, responseCheck)
			if cancel != nil {
				cancel()
			}
			select {
			case results <- queryResult{response, err, transport}:
			case <-returned:
			}
		}()
	}
	total := len(primaryTransports) + len(fallbackTransports)
	var (
		fallbackResponse  *mDNS.Msg
		fallbackTransport adapter.DNSTransport
		hasFallback       bool
		errorsList        []error
		allRejected       = true
		allRejectedOnly   = true
	)
	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			if hasFallback {
				return fallbackResponse, fallbackTransport, nil
			}
			return nil, nil, ctx.Err()
		case result := <-results:
			if result.err == nil {
				// Prefer the first NOERROR response. (Avoid using NXDOMAIN/SERVFAIL etc when a valid answer exists.)
				if result.response != nil {
					if !hasFallback {
						fallbackResponse = result.response
						fallbackTransport = result.transport
						hasFallback = true
					}
					if result.response.Rcode == mDNS.RcodeSuccess {
						queryCancel()
						return result.response, result.transport, nil
					}
				}
				continue
			}
			errorsList = append(errorsList, result.err)
			if errors.Is(result.err, ErrResponseRejectedCached) {
				// keep allRejectedOnly
			} else if errors.Is(result.err, ErrResponseRejected) {
				allRejectedOnly = false
			} else {
				allRejected = false
				allRejectedOnly = false
			}
		}
	}
	if hasFallback {
		return fallbackResponse, fallbackTransport, nil
	}
	if allRejectedOnly {
		return nil, nil, ErrResponseRejectedCached
	}
	if allRejected {
		return nil, nil, ErrResponseRejected
	}
	return nil, nil, E.Errors(errorsList...)
}

func (r *Router) lookupHedgedRacer(ctx context.Context, primaryTransports []adapter.DNSTransport, fallbackTransports []adapter.DNSTransport, domain string, primaryOptions adapter.DNSQueryOptions, fallbackOptions adapter.DNSQueryOptions, rule adapter.DNSRule, withAddressLimit bool, upstreamTimeout time.Duration, fallbackTimeout time.Duration, fallbackGrace time.Duration) ([]netip.Addr, error) {
	if upstreamTimeout <= 0 || len(fallbackTransports) == 0 {
		return r.lookupRacer(ctx, primaryTransports, domain, primaryOptions, rule, withAddressLimit)
	}
	returned := make(chan struct{})
	defer close(returned)
	type queryResult struct {
		addrs []netip.Addr
		err   error
	}
	results := make(chan queryResult)
	queryCtx, queryCancel := context.WithCancel(ctx)
	defer queryCancel()
	primaryTimeout := upstreamTimeout
	if fallbackGrace > 0 {
		primaryTimeout += fallbackGrace
	}
	primaryCtx, primaryCancel := context.WithTimeout(queryCtx, primaryTimeout)
	defer primaryCancel()
	fallbackStart := make(chan struct{})
	go func() {
		timer := time.NewTimer(upstreamTimeout)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-queryCtx.Done():
		}
		close(fallbackStart)
	}()
	for _, transport := range primaryTransports {
		transport := transport
		go func() {
			perQueryCtx := adapter.OverrideContext(primaryCtx)
			var responseCheck func(responseAddrs []netip.Addr) bool
			if withAddressLimit && rule != nil {
				metadata := adapter.ContextFrom(perQueryCtx)
				if metadata != nil {
					baseMetadata := *metadata
					responseCheck = func(responseAddrs []netip.Addr) bool {
						md := baseMetadata
						md.ResetRuleCache()
						md.DestinationAddresses = responseAddrs
						return rule.MatchAddressLimit(&md)
					}
				}
			}
			addrs, err := r.client.Lookup(perQueryCtx, transport, domain, primaryOptions, responseCheck)
			if err == nil && len(addrs) == 0 {
				err = E.New("empty result")
			}
			select {
			case results <- queryResult{addrs, err}:
			case <-returned:
			}
		}()
	}
	for _, transport := range fallbackTransports {
		transport := transport
		go func() {
			select {
			case <-fallbackStart:
			case <-queryCtx.Done():
				return
			}
			if queryCtx.Err() != nil {
				return
			}
			perQueryCtx := adapter.OverrideContext(queryCtx)
			var cancel context.CancelFunc
			if fallbackTimeout > 0 {
				perQueryCtx, cancel = context.WithTimeout(perQueryCtx, fallbackTimeout)
			}
			var responseCheck func(responseAddrs []netip.Addr) bool
			if withAddressLimit && rule != nil {
				metadata := adapter.ContextFrom(perQueryCtx)
				if metadata != nil {
					baseMetadata := *metadata
					responseCheck = func(responseAddrs []netip.Addr) bool {
						md := baseMetadata
						md.ResetRuleCache()
						md.DestinationAddresses = responseAddrs
						return rule.MatchAddressLimit(&md)
					}
				}
			}
			addrs, err := r.client.Lookup(perQueryCtx, transport, domain, fallbackOptions, responseCheck)
			if cancel != nil {
				cancel()
			}
			if err == nil && len(addrs) == 0 {
				err = E.New("empty result")
			}
			select {
			case results <- queryResult{addrs, err}:
			case <-returned:
			}
		}()
	}
	total := len(primaryTransports) + len(fallbackTransports)
	var (
		fallbackAddrs []netip.Addr
		fallbackErr   error
		hasFallback   bool
		errorsList    []error
	)
	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			if hasFallback {
				return fallbackAddrs, fallbackErr
			}
			return nil, ctx.Err()
		case result := <-results:
			if !hasFallback {
				fallbackAddrs = result.addrs
				fallbackErr = result.err
				hasFallback = true
			}
			if result.err == nil {
				queryCancel()
				return result.addrs, nil
			}
			errorsList = append(errorsList, result.err)
		}
	}
	if hasFallback {
		return fallbackAddrs, fallbackErr
	}
	return nil, E.Errors(errorsList...)
}

func (r *Router) exchangeRacer(ctx context.Context, transports []adapter.DNSTransport, message *mDNS.Msg, options adapter.DNSQueryOptions, rule adapter.DNSRule, withAddressLimit bool) (*mDNS.Msg, adapter.DNSTransport, error) {
	returned := make(chan struct{})
	defer close(returned)
	type queryResult struct {
		response  *mDNS.Msg
		err       error
		transport adapter.DNSTransport
	}
	results := make(chan queryResult)
	queryCtx, queryCancel := context.WithCancel(ctx)
	defer queryCancel()
	for _, transport := range transports {
		transport := transport
		go func() {
			perQueryCtx := adapter.OverrideContext(queryCtx)
			msgCopy := message.Copy()
			var responseCheck func(responseAddrs []netip.Addr) bool
			if withAddressLimit && rule != nil {
				metadata := adapter.ContextFrom(perQueryCtx)
				if metadata != nil {
					baseMetadata := *metadata
					responseCheck = func(responseAddrs []netip.Addr) bool {
						md := baseMetadata
						md.ResetRuleCache()
						md.DestinationAddresses = responseAddrs
						return rule.MatchAddressLimit(&md)
					}
				}
			}
			response, err := r.client.Exchange(perQueryCtx, transport, msgCopy, options, responseCheck)
			select {
			case results <- queryResult{response, err, transport}:
			case <-returned:
			}
		}()
	}
	var (
		fallbackResponse  *mDNS.Msg
		fallbackTransport adapter.DNSTransport
		hasFallback       bool
		errorsList        []error
		allRejected       = true
		allRejectedOnly   = true
	)
	for i := 0; i < len(transports); i++ {
		select {
		case <-ctx.Done():
			if hasFallback {
				return fallbackResponse, fallbackTransport, nil
			}
			return nil, nil, ctx.Err()
		case result := <-results:
			if result.err == nil {
				// Prefer the first NOERROR response. (Avoid using NXDOMAIN/SERVFAIL etc when a valid answer exists.)
				if result.response != nil {
					if !hasFallback {
						fallbackResponse = result.response
						fallbackTransport = result.transport
						hasFallback = true
					}
					if result.response.Rcode == mDNS.RcodeSuccess {
						queryCancel()
						return result.response, result.transport, nil
					}
				}
				continue
			}
			errorsList = append(errorsList, result.err)
			if errors.Is(result.err, ErrResponseRejectedCached) {
				// keep allRejectedOnly
			} else if errors.Is(result.err, ErrResponseRejected) {
				allRejectedOnly = false
			} else {
				allRejected = false
				allRejectedOnly = false
			}
		}
	}
	if hasFallback {
		return fallbackResponse, fallbackTransport, nil
	}
	if allRejectedOnly {
		return nil, nil, ErrResponseRejectedCached
	}
	if allRejected {
		return nil, nil, ErrResponseRejected
	}
	return nil, nil, E.Errors(errorsList...)
}

func (r *Router) lookupRacer(ctx context.Context, transports []adapter.DNSTransport, domain string, options adapter.DNSQueryOptions, rule adapter.DNSRule, withAddressLimit bool) ([]netip.Addr, error) {
	returned := make(chan struct{})
	defer close(returned)
	type queryResult struct {
		addrs []netip.Addr
		err   error
	}
	results := make(chan queryResult)
	queryCtx, queryCancel := context.WithCancel(ctx)
	defer queryCancel()
	for _, transport := range transports {
		transport := transport
		go func() {
			perQueryCtx := adapter.OverrideContext(queryCtx)
			var responseCheck func(responseAddrs []netip.Addr) bool
			if withAddressLimit && rule != nil {
				metadata := adapter.ContextFrom(perQueryCtx)
				if metadata != nil {
					baseMetadata := *metadata
					responseCheck = func(responseAddrs []netip.Addr) bool {
						md := baseMetadata
						md.ResetRuleCache()
						md.DestinationAddresses = responseAddrs
						return rule.MatchAddressLimit(&md)
					}
				}
			}
			addrs, err := r.client.Lookup(perQueryCtx, transport, domain, options, responseCheck)
			if err == nil && len(addrs) == 0 {
				err = E.New("empty result")
			}
			select {
			case results <- queryResult{addrs, err}:
			case <-returned:
			}
		}()
	}
	var (
		fallbackAddrs []netip.Addr
		fallbackErr   error
		hasFallback   bool
		errorsList    []error
	)
	for i := 0; i < len(transports); i++ {
		select {
		case <-ctx.Done():
			if hasFallback {
				return fallbackAddrs, fallbackErr
			}
			return nil, ctx.Err()
		case result := <-results:
			if !hasFallback {
				fallbackAddrs = result.addrs
				fallbackErr = result.err
				hasFallback = true
			}
			if result.err == nil {
				queryCancel()
				return result.addrs, nil
			}
			errorsList = append(errorsList, result.err)
		}
	}
	if hasFallback {
		return fallbackAddrs, fallbackErr
	}
	return nil, E.Errors(errorsList...)
}

func isAddressQuery(message *mDNS.Msg) bool {
	for _, question := range message.Question {
		if question.Qtype == mDNS.TypeA || question.Qtype == mDNS.TypeAAAA || question.Qtype == mDNS.TypeHTTPS {
			return true
		}
	}
	return false
}

func (r *Router) ClearCache() {
	r.client.ClearCache()
	if r.platformInterface != nil {
		r.platformInterface.ClearDNSCache()
	}
}

func (r *Router) LookupReverseMapping(ip netip.Addr) (string, bool) {
	if r.dnsReverseMapping == nil {
		return "", false
	}
	domain, loaded := r.dnsReverseMapping.Get(ip)
	return domain, loaded
}

func (r *Router) ResetNetwork() {
	r.ClearCache()
	for _, transport := range r.transport.Transports() {
		transport.Close()
	}
}
