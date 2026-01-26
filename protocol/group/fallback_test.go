package group

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
)

func TestFallbackDial_FailoverAndRecover(t *testing.T) {
	primary := newFakeOutbound("primary", "tcp")
	backup := newFakeOutbound("backup", "tcp")
	primary.SetDialError(errors.New("dial failed"))

	group, err := NewFallbackGroup(context.Background(), nil, log.StdLogger(), []adapter.Outbound{primary, backup}, "", 0, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	group.checker.outbounds = nil
	group.checker.history.StoreURLTestHistory(primary.Tag(), &adapter.URLTestHistory{Time: time.Now(), Delay: 10})
	group.checker.history.StoreURLTestHistory(backup.Tag(), &adapter.URLTestHistory{Time: time.Now(), Delay: 20})

	conn, err := group.DialContext(context.Background(), "tcp", M.ParseSocksaddrHostPort("example.com", 80))
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if primary.DialCalls() != 1 || backup.DialCalls() != 1 {
		t.Fatalf("unexpected dial calls: primary=%d backup=%d", primary.DialCalls(), backup.DialCalls())
	}
	if group.Now() != backup.Tag() {
		t.Fatalf("unexpected now: %s", group.Now())
	}
	if group.checker.history.LoadURLTestHistory(primary.Tag()) != nil {
		t.Fatalf("primary should be marked unavailable")
	}

	primary.SetDialError(nil)
	group.checker.history.StoreURLTestHistory(primary.Tag(), &adapter.URLTestHistory{Time: time.Now(), Delay: 10})
	group.performUpdateCheck()
	if group.Now() != primary.Tag() {
		t.Fatalf("expected fallback to recover to primary, got %s", group.Now())
	}
}
