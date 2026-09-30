package sfu

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestResolverStaticIPv4(t *testing.T) {
	ctx := context.Background()
	r, err := NewResolver(ctx, ResolverOptions{
		Host: "198.51.100.5",
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	defer r.Close()

	if got := r.IP(); got != "198.51.100.5" {
		t.Errorf("IP = %q, want %q", got, "198.51.100.5")
	}
}

func TestResolverEmptyHost(t *testing.T) {
	ctx := context.Background()
	r, err := NewResolver(ctx, ResolverOptions{Host: ""})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	defer r.Close()

	if got := r.IP(); got != "" {
		t.Errorf("IP = %q, want empty", got)
	}
}

func TestResolverDomainSuccessAndRefresh(t *testing.T) {
	ctx := context.Background()
	var callCount atomic.Int32

	lookup := func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if host != "sfu.example.com" {
			return nil, errors.New("unexpected host")
		}
		count := callCount.Add(1)
		if count == 1 {
			return []netip.Addr{netip.MustParseAddr("198.51.100.1")}, nil
		}
		if count == 2 {
			// Failed refresh should not clear old IP
			return nil, errors.New("temporary DNS failure")
		}
		return []netip.Addr{netip.MustParseAddr("198.51.100.2")}, nil
	}

	r, err := NewResolver(ctx, ResolverOptions{
		Host:        "sfu.example.com",
		Interval:    10 * time.Millisecond,
		LookupNetIP: lookup,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	defer r.Close()

	if got := r.IP(); got != "198.51.100.1" {
		t.Errorf("initial IP = %q, want 198.51.100.1", got)
	}

	// Wait for ticker to run at least twice (failure then update to .2)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if r.IP() == "198.51.100.2" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := r.IP(); got != "198.51.100.2" {
		t.Errorf("updated IP = %q, want 198.51.100.2", got)
	}
}

func TestResolverInitialFailure(t *testing.T) {
	ctx := context.Background()
	lookup := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return nil, errors.New("host not found")
	}

	_, err := NewResolver(ctx, ResolverOptions{
		Host:        "invalid.example.com",
		LookupNetIP: lookup,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolverNoIPv4(t *testing.T) {
	ctx := context.Background()
	lookup := func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("2001:db8::1")}, nil
	}

	_, err := NewResolver(ctx, ResolverOptions{
		Host:        "ipv6only.example.com",
		LookupNetIP: lookup,
	})
	if err == nil {
		t.Fatal("expected error for IPv6-only host, got nil")
	}
}
