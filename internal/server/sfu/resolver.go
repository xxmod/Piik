package sfu

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"time"
)

const defaultRefreshInterval = 24 * time.Hour

// ResolverOptions configures the SFU public host resolver.
type ResolverOptions struct {
	Host     string
	Interval time.Duration
	Logger   *slog.Logger
	// LookupNetIP allows tests to supply a mock DNS resolver.
	LookupNetIP func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Resolver resolves an SFU public IP or domain name and refreshes domain names periodically.
type Resolver struct {
	host        string
	interval    time.Duration
	logger      *slog.Logger
	lookupNetIP func(ctx context.Context, network, host string) ([]netip.Addr, error)
	currentIP   atomic.Pointer[string]
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewResolver initializes the resolver and resolves the host immediately.
func NewResolver(ctx context.Context, options ResolverOptions) (*Resolver, error) {
	if options.Host == "" {
		return &Resolver{}, nil
	}
	lookup := options.LookupNetIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupNetIP
	}
	interval := options.Interval
	if interval <= 0 {
		interval = defaultRefreshInterval
	}

	// Static IPv4 address: store directly and do not run periodic DNS lookup.
	if addr, err := netip.ParseAddr(options.Host); err == nil && addr.Is4() {
		r := &Resolver{
			host:        options.Host,
			interval:    interval,
			logger:      options.Logger,
			lookupNetIP: lookup,
		}
		ip := addr.String()
		r.currentIP.Store(&ip)
		return r, nil
	}

	// Hostname / domain: perform initial resolve synchronously.
	addrs, err := lookup(ctx, "ip4", options.Host)
	if err != nil {
		return nil, fmt.Errorf("resolve SFU public host %q: %w", options.Host, err)
	}
	var initialIP string
	for _, addr := range addrs {
		if addr.Is4() {
			initialIP = addr.String()
			break
		}
	}
	if initialIP == "" {
		return nil, fmt.Errorf("no IPv4 address found for SFU public host %q", options.Host)
	}

	bgCtx, cancel := context.WithCancel(context.Background())
	r := &Resolver{
		host:        options.Host,
		interval:    interval,
		logger:      options.Logger,
		lookupNetIP: lookup,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	r.currentIP.Store(&initialIP)
	go r.run(bgCtx)
	return r, nil
}

func (r *Resolver) run(ctx context.Context) {
	defer close(r.done)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

func (r *Resolver) refresh(ctx context.Context) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := r.lookupNetIP(timeoutCtx, "ip4", r.host)
	if err != nil {
		if r.logger != nil {
			r.logger.Warn("sfu-public-ip-refresh-failed", "host", r.host, "error", err)
		}
		return
	}
	for _, addr := range addrs {
		if addr.Is4() {
			newIP := addr.String()
			oldPtr := r.currentIP.Load()
			if oldPtr == nil || *oldPtr != newIP {
				r.currentIP.Store(&newIP)
				if r.logger != nil {
					old := ""
					if oldPtr != nil {
						old = *oldPtr
					}
					r.logger.Info("sfu-public-ip-updated", "host", r.host, "old", old, "new", newIP)
				}
			}
			return
		}
	}
	if r.logger != nil {
		r.logger.Warn("sfu-public-ip-refresh-no-ipv4", "host", r.host)
	}
}

// IP returns the latest resolved IPv4 address, or empty string if not configured.
func (r *Resolver) IP() string {
	if r == nil {
		return ""
	}
	ptr := r.currentIP.Load()
	if ptr == nil {
		return ""
	}
	return *ptr
}

// Close stops background refresh goroutine if running.
func (r *Resolver) Close() {
	if r != nil && r.cancel != nil {
		r.cancel()
		<-r.done
	}
}
