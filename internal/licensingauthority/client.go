package licensingauthority

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LicenseAuthorityClient defines the contract for authority communication.
// No real HTTP calls are implemented — interfaces only.
type LicenseAuthorityClient interface {
	Validate(ctx context.Context, req *ValidationRequest) (*ValidationResponse, error)
	Activate(ctx context.Context, req *ActivationRequest) (*ActivationResponse, error)
	Heartbeat(ctx context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error)
	Entitlements(ctx context.Context, req *EntitlementRequest) (*EntitlementResponse, error)
}

// NoopAuthorityClient returns safe defaults, never calls network.
type NoopAuthorityClient struct{}

func (n *NoopAuthorityClient) Validate(_ context.Context, _ *ValidationRequest) (*ValidationResponse, error) {
	return &ValidationResponse{Valid: true, LicenseState: LicenseValid, ValidatedAt: time.Now()}, nil
}

func (n *NoopAuthorityClient) Activate(_ context.Context, _ *ActivationRequest) (*ActivationResponse, error) {
	return &ActivationResponse{Activated: true}, nil
}

func (n *NoopAuthorityClient) Heartbeat(_ context.Context, _ *HeartbeatRequest) (*HeartbeatResponse, error) {
	return &HeartbeatResponse{Acknowledged: true}, nil
}

func (n *NoopAuthorityClient) Entitlements(_ context.Context, _ *EntitlementRequest) (*EntitlementResponse, error) {
	return &EntitlementResponse{
		LicenseID: "noop-license",
		Edition:   "community",
		Features:  []string{},
		Limits:    EntitlementLimits{MaxDomains: 1, MaxMailboxes: 5, MaxStorageGB: 1},
	}, nil
}

// FakeAuthorityClient is a configurable client for tests.
type FakeAuthorityClient struct {
	mu             sync.RWMutex
	validateFn     func(*ValidationRequest) (*ValidationResponse, error)
	activateFn     func(*ActivationRequest) (*ActivationResponse, error)
	heartbeatFn    func(*HeartbeatRequest) (*HeartbeatResponse, error)
	entitlementsFn func(*EntitlementRequest) (*EntitlementResponse, error)
}

func NewFakeAuthorityClient() *FakeAuthorityClient {
	return &FakeAuthorityClient{
		validateFn: func(r *ValidationRequest) (*ValidationResponse, error) {
			return &ValidationResponse{Valid: true, LicenseState: LicenseValid, ValidatedAt: time.Now()}, nil
		},
		activateFn: func(r *ActivationRequest) (*ActivationResponse, error) {
			return &ActivationResponse{Activated: true}, nil
		},
		heartbeatFn: func(r *HeartbeatRequest) (*HeartbeatResponse, error) {
			return &HeartbeatResponse{Acknowledged: true}, nil
		},
		entitlementsFn: func(r *EntitlementRequest) (*EntitlementResponse, error) {
			return &EntitlementResponse{
				LicenseID: "fake-license",
				Edition:   "professional",
				Features:  []string{"smtp", "imap", "pop3"},
				Limits:    EntitlementLimits{MaxDomains: 50, MaxMailboxes: 500, MaxStorageGB: 50},
			}, nil
		},
	}
}

func (f *FakeAuthorityClient) SetValidateFn(fn func(*ValidationRequest) (*ValidationResponse, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validateFn = fn
}

func (f *FakeAuthorityClient) SetActivateFn(fn func(*ActivationRequest) (*ActivationResponse, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activateFn = fn
}

func (f *FakeAuthorityClient) SetHeartbeatFn(fn func(*HeartbeatRequest) (*HeartbeatResponse, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeatFn = fn
}

func (f *FakeAuthorityClient) SetEntitlementsFn(fn func(*EntitlementRequest) (*EntitlementResponse, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entitlementsFn = fn
}

func (f *FakeAuthorityClient) Validate(_ context.Context, req *ValidationRequest) (*ValidationResponse, error) {
	f.mu.RLock()
	fn := f.validateFn
	f.mu.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("no validate function set")
	}
	return fn(req)
}

func (f *FakeAuthorityClient) Activate(_ context.Context, req *ActivationRequest) (*ActivationResponse, error) {
	f.mu.RLock()
	fn := f.activateFn
	f.mu.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("no activate function set")
	}
	return fn(req)
}

func (f *FakeAuthorityClient) Heartbeat(_ context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error) {
	f.mu.RLock()
	fn := f.heartbeatFn
	f.mu.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("no heartbeat function set")
	}
	return fn(req)
}

func (f *FakeAuthorityClient) Entitlements(_ context.Context, req *EntitlementRequest) (*EntitlementResponse, error) {
	f.mu.RLock()
	fn := f.entitlementsFn
	f.mu.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("no entitlements function set")
	}
	return fn(req)
}
