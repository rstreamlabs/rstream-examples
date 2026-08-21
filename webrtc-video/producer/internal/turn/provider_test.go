package turn

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rstreamlabs/rstream-go"
)

func TestCloneCredentialsCopiesURLs(t *testing.T) {
	original := &rstream.TURNCredentials{
		URLs:       []string{"turn:one.example.com"},
		Username:   "viewer",
		Credential: "secret",
		TTL:        3600,
	}
	clone := cloneCredentials(original)
	clone.URLs[0] = "turn:mutated.example.com"
	if original.URLs[0] != "turn:one.example.com" {
		t.Fatalf("expected the cloned URL slice to be independent, got %q", original.URLs[0])
	}
}

func TestCachedCredentialsReportTheirRemainingLifetime(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	provider := &Provider{
		cached:     &rstream.TURNCredentials{URLs: []string{"turn:relay.example:3478?transport=udp"}, Username: "viewer", Credential: "secret", TTL: 600},
		refreshAt:  now.Add(5 * time.Minute),
		validUntil: now.Add(10 * time.Minute),
		now:        func() time.Time { return now.Add(4*time.Minute + 500*time.Millisecond) },
	}
	credentials, err := provider.Credentials(context.Background())
	if err != nil {
		t.Fatalf("load cached credentials: %v", err)
	}
	if credentials.TTL != 360 {
		t.Fatalf("remaining TURN TTL = %d, want 360", credentials.TTL)
	}
	credentials.URLs[0] = "turn:mutated.example"
	if provider.cached.URLs[0] != "turn:relay.example:3478?transport=udp" {
		t.Fatal("cached TURN URLs were exposed by reference")
	}
}

func TestCredentialRefreshAtPreservesShortLivedCache(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	for _, test := range []struct {
		name string
		ttl  int
		want time.Duration
	}{
		{name: "one minute", ttl: 60, want: 30 * time.Second},
		{name: "ten minutes", ttl: 600, want: 5 * time.Minute},
		{name: "one hour", ttl: 3600, want: 55 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			credentials := &rstream.TURNCredentials{TTL: test.ttl}
			if got := credentialsRefreshAt(credentials, now).Sub(now); got != test.want {
				t.Fatalf("refresh delay = %s, want %s", got, test.want)
			}
		})
	}
}

func TestCredentialRefreshAtRejectsMissingLifetime(t *testing.T) {
	now := time.Unix(1_900_000_000, 0)
	for _, credentials := range []*rstream.TURNCredentials{nil, {TTL: 0}, {TTL: -1}} {
		if got := credentialsRefreshAt(credentials, now); !got.Equal(now) {
			t.Fatalf("refresh time = %s, want %s", got, now)
		}
	}
}

func TestConcurrentCredentialRefreshDoesNotHoldTheCacheLock(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Uint64
	provider := &Provider{
		fetch: func(ctx context.Context) (*rstream.TURNCredentials, error) {
			calls.Add(1)
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return &rstream.TURNCredentials{URLs: []string{"turn:relay.example:3478?transport=udp"}, Username: "viewer", Credential: "secret", TTL: 600}, nil
			}
		},
		now: func() time.Time { return now },
	}
	leader := make(chan *rstream.TURNCredentials, 1)
	go func() {
		credentials, _ := provider.Credentials(context.Background())
		leader <- credentials
	}()
	waitForSignal(t, started)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Credentials(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v, want context cancellation", err)
	}
	waiter := make(chan *rstream.TURNCredentials, 1)
	go func() {
		credentials, _ := provider.Credentials(context.Background())
		waiter <- credentials
	}()
	close(release)
	first := waitForCredentials(t, leader)
	second := waitForCredentials(t, waiter)
	if calls.Load() != 1 || first == nil || second == nil {
		t.Fatalf("refresh calls = %d, credentials = %v / %v", calls.Load(), first, second)
	}
	first.URLs[0] = "turn:mutated.example"
	if second.URLs[0] != "turn:relay.example:3478?transport=udp" {
		t.Fatal("concurrent callers shared a mutable TURN URL slice")
	}
}

func TestCredentialRefreshOutlivesTheCallerThatStartedIt(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Uint64
	provider := &Provider{
		fetch: func(ctx context.Context) (*rstream.TURNCredentials, error) {
			calls.Add(1)
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return &rstream.TURNCredentials{URLs: []string{"turn:relay.example:3478?transport=udp"}, Username: "viewer", Credential: "secret", TTL: 600}, nil
			}
		},
		now:            time.Now,
		refreshTimeout: time.Second,
	}
	leaderContext, cancelLeader := context.WithCancel(context.Background())
	leaderResult := make(chan error, 1)
	go func() {
		_, err := provider.Credentials(leaderContext)
		leaderResult <- err
	}()
	waitForSignal(t, started)
	waiter := make(chan *rstream.TURNCredentials, 1)
	go func() {
		credentials, _ := provider.Credentials(context.Background())
		waiter <- credentials
	}()
	cancelLeader()
	if err := <-leaderResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("refresh leader error = %v, want context cancellation", err)
	}
	close(release)
	if credentials := waitForCredentials(t, waiter); credentials == nil {
		t.Fatal("shared refresh was canceled with its first caller")
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
}

func TestCredentialRefreshHasAnIndependentDeadline(t *testing.T) {
	provider := &Provider{
		fetch: func(ctx context.Context) (*rstream.TURNCredentials, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		now:            time.Now,
		refreshTimeout: time.Millisecond,
	}
	started := time.Now()
	_, err := provider.Credentials(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("refresh error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("refresh deadline took %s", elapsed)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("TURN refresh did not reach its synchronization point")
	}
}

func waitForCredentials(t *testing.T, result <-chan *rstream.TURNCredentials) *rstream.TURNCredentials {
	t.Helper()
	select {
	case credentials := <-result:
		return credentials
	case <-time.After(time.Second):
		t.Fatal("TURN credential refresh did not complete")
		return nil
	}
}

func TestFilterCredentialsSelectsExplicitTransports(t *testing.T) {
	original := &rstream.TURNCredentials{
		URLs: []string{
			"turn:turn.example.com:3478?transport=udp",
			"turn:turn.example.com:3478?transport=tcp",
			"turns:turn.example.com:5349?transport=udp",
			"turns:turn.example.com:5349?transport=tcp",
		},
		Username:   "viewer",
		Credential: "secret",
		TTL:        600,
	}
	filtered, err := filterCredentials(original, map[string]struct{}{
		"udp": {},
		"tls": {},
	})
	if err != nil {
		t.Fatalf("filter credentials: %v", err)
	}
	want := []string{
		"turn:turn.example.com:3478?transport=udp",
		"turns:turn.example.com:5349?transport=tcp",
	}
	if !reflect.DeepEqual(filtered.URLs, want) {
		t.Fatalf("filtered URLs = %v, want %v", filtered.URLs, want)
	}
	if len(original.URLs) != 4 {
		t.Fatal("filter changed the original credentials")
	}
}

func TestFilterCredentialsRejectsUnsupportedOrMissingTransports(t *testing.T) {
	tests := []struct {
		name string
		urls []string
	}{
		{name: "malformed", urls: []string{"https://turn.example.com"}},
		{name: "missing", urls: []string{"turn:turn.example.com:3478?transport=udp"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := filterCredentials(&rstream.TURNCredentials{URLs: tc.urls}, map[string]struct{}{"tls": {}})
			if err == nil {
				t.Fatal("expected transport filter failure")
			}
		})
	}
}

func TestClassifyTURNTransport(t *testing.T) {
	tests := map[string]string{
		"turn:turn.example.com:3478?transport=udp":  "udp",
		"turn:turn.example.com:3478?transport=tcp":  "tcp",
		"turns:turn.example.com:5349?transport=udp": "dtls",
		"turns:turn.example.com:5349?transport=tcp": "tls",
	}
	for rawURL, want := range tests {
		got, err := classifyTURNTransport(rawURL)
		if err != nil {
			t.Fatalf("classify %q: %v", rawURL, err)
		}
		if got != want {
			t.Fatalf("classify %q = %q, want %q", rawURL, got, want)
		}
	}
}

func TestFilterCredentialsValidatesUnrestrictedURLs(t *testing.T) {
	if _, err := filterCredentials(&rstream.TURNCredentials{
		URLs: []string{"https://turn.example.com"},
	}, nil); err == nil {
		t.Fatal("expected an unsupported unrestricted TURN URL to fail")
	}
}

func TestFilterCredentialsRejectsMissingCredentials(t *testing.T) {
	if _, err := filterCredentials(nil, nil); err == nil {
		t.Fatal("expected missing TURN credentials to fail")
	}
}

func TestClassifyTURNTransportRejectsMissingEndpoint(t *testing.T) {
	if _, err := classifyTURNTransport("turn:?transport=udp"); err == nil {
		t.Fatal("expected an empty TURN endpoint to fail")
	}
}

func TestURLsByTransportIndexesEveryFallback(t *testing.T) {
	credentials := &rstream.TURNCredentials{URLs: []string{
		"turn:turn.example.com:3478?transport=udp",
		"turn:turn.example.com:3478?transport=tcp",
		"turns:turn.example.com:5349?transport=udp",
		"turns:turn.example.com:5349?transport=tcp",
	}}
	urls, err := URLsByTransport(credentials)
	if err != nil {
		t.Fatalf("index TURN URLs: %v", err)
	}
	if len(urls) != 4 {
		t.Fatalf("indexed transports = %d, want 4", len(urls))
	}
	if got := urls["dtls"]; got != credentials.URLs[2] {
		t.Fatalf("DTLS URL = %q, want %q", got, credentials.URLs[2])
	}
	if got := urls["tls"]; got != credentials.URLs[3] {
		t.Fatalf("TLS URL = %q, want %q", got, credentials.URLs[3])
	}
}
