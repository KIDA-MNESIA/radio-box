package group

import (
	"context"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

func TestLoadBalance_RoundRobin(t *testing.T) {
	a := newFakeOutbound("a", "tcp")
	b := newFakeOutbound("b", "tcp")

	group, err := NewLoadBalanceGroup(context.Background(), nil, log.StdLogger(), []adapter.Outbound{a, b}, "", 0, 0, 0, option.LoadBalanceStrategyRoundRobin)
	if err != nil {
		t.Fatal(err)
	}
	group.checker.outbounds = nil

	dest := M.ParseSocksaddrHostPort("example.com", 80)
	c1, err := group.DialContext(context.Background(), "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	_ = c1.Close()
	c2, err := group.DialContext(context.Background(), "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	_ = c2.Close()

	if a.DialCalls() != 1 || b.DialCalls() != 1 {
		t.Fatalf("expected round-robin distribution, got a=%d b=%d", a.DialCalls(), b.DialCalls())
	}
}

func TestLoadBalance_ConsistentHashing_TopLevelDomain(t *testing.T) {
	a := newFakeOutbound("a", "tcp")
	b := newFakeOutbound("b", "tcp")

	group, err := NewLoadBalanceGroup(context.Background(), nil, log.StdLogger(), []adapter.Outbound{a, b}, "", 0, 0, 0, option.LoadBalanceStrategyConsistentHashing)
	if err != nil {
		t.Fatal(err)
	}
	group.checker.outbounds = nil

	dest1 := M.ParseSocksaddrHostPort("a.example.co.uk", 80)
	dest2 := M.ParseSocksaddrHostPort("b.example.co.uk", 80)
	c1, err := group.DialContext(context.Background(), "tcp", dest1)
	if err != nil {
		t.Fatal(err)
	}
	_ = c1.Close()
	c2, err := group.DialContext(context.Background(), "tcp", dest2)
	if err != nil {
		t.Fatal(err)
	}
	_ = c2.Close()

	if !(a.DialCalls() == 2 && b.DialCalls() == 0) && !(a.DialCalls() == 0 && b.DialCalls() == 2) {
		t.Fatalf("expected same outbound for same base domain, got a=%d b=%d", a.DialCalls(), b.DialCalls())
	}
}

func TestLoadBalance_StickySessions(t *testing.T) {
	a := newFakeOutbound("a", "tcp")
	b := newFakeOutbound("b", "tcp")

	group, err := NewLoadBalanceGroup(context.Background(), nil, log.StdLogger(), []adapter.Outbound{a, b}, "", 0, 0, 0, option.LoadBalanceStrategyStickySessions)
	if err != nil {
		t.Fatal(err)
	}
	group.checker.outbounds = nil

	dest := M.ParseSocksaddrHostPort("example.com", 80)
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{
		Source: M.ParseSocksaddrHostPort("10.0.0.1", 12345),
	})
	c1, err := group.DialContext(ctx, "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	_ = c1.Close()
	c2, err := group.DialContext(ctx, "tcp", dest)
	if err != nil {
		t.Fatal(err)
	}
	_ = c2.Close()

	if !(a.DialCalls() == 2 && b.DialCalls() == 0) && !(a.DialCalls() == 0 && b.DialCalls() == 2) {
		t.Fatalf("expected sticky mapping, got a=%d b=%d", a.DialCalls(), b.DialCalls())
	}
}
