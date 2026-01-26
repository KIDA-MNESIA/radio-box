package group

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/urltest"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/batch"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/x/list"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

type healthChecker struct {
	ctx      context.Context
	outbound adapter.OutboundManager
	pause    pause.Manager

	pauseCallback *list.Element[pause.Callback]

	logger      log.Logger
	outbounds   []adapter.Outbound
	link        string
	interval    time.Duration
	idleTimeout time.Duration
	timeout     time.Duration

	history adapter.URLTestHistoryStorage

	checking atomic.Bool

	access     sync.Mutex
	ticker     *time.Ticker
	close      chan struct{}
	started    bool
	lastActive common.TypedValue[time.Time]

	onUpdate func()
}

func newHealthChecker(ctx context.Context, outboundManager adapter.OutboundManager, logger log.Logger, outbounds []adapter.Outbound, link string, interval time.Duration, idleTimeout time.Duration, timeout time.Duration, onUpdate func()) (*healthChecker, error) {
	if interval == 0 {
		interval = C.DefaultURLTestInterval
	}
	if idleTimeout == 0 {
		idleTimeout = C.DefaultURLTestIdleTimeout
	}
	if timeout == 0 {
		timeout = C.TCPTimeout
	}
	if interval > idleTimeout {
		return nil, E.New("interval must be less or equal than idle_timeout")
	}
	var history adapter.URLTestHistoryStorage
	if historyFromCtx := service.PtrFromContext[urltest.HistoryStorage](ctx); historyFromCtx != nil {
		history = historyFromCtx
	} else if clashServer := service.FromContext[adapter.ClashServer](ctx); clashServer != nil {
		history = clashServer.HistoryStorage()
	} else {
		history = urltest.NewHistoryStorage()
	}
	return &healthChecker{
		ctx:         ctx,
		outbound:    outboundManager,
		pause:       service.FromContext[pause.Manager](ctx),
		logger:      logger,
		outbounds:   outbounds,
		link:        link,
		interval:    interval,
		idleTimeout: idleTimeout,
		timeout:     timeout,
		history:     history,
		close:       make(chan struct{}),
		onUpdate:    onUpdate,
	}, nil
}

func (h *healthChecker) PostStart() {
	h.access.Lock()
	defer h.access.Unlock()
	h.started = true
	h.lastActive.Store(time.Now())
	go h.CheckOutbounds(false)
}

func (h *healthChecker) Touch() {
	if !h.started {
		return
	}
	h.access.Lock()
	defer h.access.Unlock()
	if h.ticker != nil {
		h.lastActive.Store(time.Now())
		return
	}
	h.ticker = time.NewTicker(h.interval)
	go h.loopCheck()
	h.pauseCallback = pause.RegisterTicker(h.pause, h.ticker, h.interval, nil)
}

func (h *healthChecker) Close() error {
	h.access.Lock()
	defer h.access.Unlock()
	if h.ticker == nil {
		return nil
	}
	h.ticker.Stop()
	h.pause.UnregisterCallback(h.pauseCallback)
	close(h.close)
	return nil
}

func (h *healthChecker) loopCheck() {
	if time.Since(h.lastActive.Load()) > h.interval {
		h.lastActive.Store(time.Now())
		h.CheckOutbounds(false)
	}
	for {
		select {
		case <-h.close:
			return
		case <-h.ticker.C:
		}
		if time.Since(h.lastActive.Load()) > h.idleTimeout {
			h.access.Lock()
			h.ticker.Stop()
			h.ticker = nil
			h.pause.UnregisterCallback(h.pauseCallback)
			h.pauseCallback = nil
			h.access.Unlock()
			return
		}
		h.CheckOutbounds(false)
	}
}

func (h *healthChecker) CheckOutbounds(force bool) {
	_, _ = h.urlTest(h.ctx, force)
}

func (h *healthChecker) urlTest(ctx context.Context, force bool) (map[string]uint16, error) {
	result := make(map[string]uint16)
	if h.checking.Swap(true) {
		return result, nil
	}
	defer h.checking.Store(false)
	b, _ := batch.New(ctx, batch.WithConcurrencyNum[any](10))
	checked := make(map[string]bool)
	var resultAccess sync.Mutex
	for _, detour := range h.outbounds {
		tag := detour.Tag()
		realTag := RealTag(detour)
		if realTag == "" || checked[realTag] {
			continue
		}
		history := h.history.LoadURLTestHistory(realTag)
		if !force && history != nil && time.Since(history.Time) < h.interval {
			continue
		}
		checked[realTag] = true
		p, loaded := h.outbound.Outbound(realTag)
		if !loaded {
			continue
		}
		b.Go(realTag, func() (any, error) {
			testCtx, cancel := context.WithTimeout(ctx, h.timeout)
			defer cancel()
			t, err := urltest.URLTest(testCtx, h.link, p)
			if err != nil {
				h.logger.Debug("outbound ", tag, " unavailable: ", err)
				h.history.DeleteURLTestHistory(realTag)
			} else {
				h.logger.Debug("outbound ", tag, " available: ", t, "ms")
				h.history.StoreURLTestHistory(realTag, &adapter.URLTestHistory{
					Time:  time.Now(),
					Delay: t,
				})
				resultAccess.Lock()
				result[tag] = t
				resultAccess.Unlock()
			}
			return nil, nil
		})
	}
	b.Wait()
	if h.onUpdate != nil {
		h.onUpdate()
	}
	return result, nil
}
