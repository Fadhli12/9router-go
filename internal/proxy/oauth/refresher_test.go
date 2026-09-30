package oauth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedOAuthClientConfiguration(t *testing.T) {
	if DefaultOAuthClient == nil {
		t.Fatal("DefaultOAuthClient is nil")
	}
	if DefaultClient != DefaultOAuthClient {
		t.Errorf("DefaultClient should alias DefaultOAuthClient")
	}
	if DefaultOAuthClient.Timeout != 30*time.Second {
		t.Errorf("expected Timeout 30s, got %v", DefaultOAuthClient.Timeout)
	}

	tr, ok := DefaultOAuthClient.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatal("expected DefaultOAuthClient.Transport to be *http.Transport")
	}
	if DefaultTransport != DefaultOAuthTransport {
		t.Errorf("DefaultTransport should alias DefaultOAuthTransport")
	}
	if tr.MaxIdleConns != 100 {
		t.Errorf("expected MaxIdleConns=100, got %d", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 10 {
		t.Errorf("expected MaxIdleConnsPerHost=10, got %d", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 90*time.Second {
		t.Errorf("expected IdleConnTimeout=90s, got %v", tr.IdleConnTimeout)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Errorf("expected ForceAttemptHTTP2 to be true")
	}
	if tr.DialContext == nil {
		t.Errorf("expected DialContext to be configured with keep-alive")
	}
}

func TestRefresh_SingleflightDeduplication(t *testing.T) {
	testProvider := "test-singleflight-dedup"
	var callCount atomic.Int32

	Register(testProvider, func(ctx context.Context, p *Params) (*TokenResult, error) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate upstream latency
		return &TokenResult{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    3600,
		}, nil
	})

	const concurrentCalls = 10
	var wg sync.WaitGroup
	results := make([]*TokenResult, concurrentCalls)
	errs := make([]error, concurrentCalls)

	for i := 0; i < concurrentCalls; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := Refresh(context.Background(), &Params{
				Provider:     testProvider,
				RefreshToken: "shared-refresh-token",
			})
			results[idx] = res
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	if got := callCount.Load(); got != 1 {
		t.Fatalf("expected refresher to be called exactly 1 time, got %d", got)
	}

	for i := 0; i < concurrentCalls; i++ {
		if errs[i] != nil {
			t.Errorf("call %d returned unexpected error: %v", i, errs[i])
		}
		if results[i] == nil || results[i].AccessToken != "new-access-token" {
			t.Errorf("call %d returned invalid result: %+v", i, results[i])
		}
	}
}

func TestRefresh_DifferentTokensIndependent(t *testing.T) {
	testProvider := "test-singleflight-different-tokens"
	var callCount atomic.Int32

	Register(testProvider, func(ctx context.Context, p *Params) (*TokenResult, error) {
		callCount.Add(1)
		time.Sleep(30 * time.Millisecond)
		return &TokenResult{
			AccessToken:  "token-" + p.RefreshToken,
			RefreshToken: "next-" + p.RefreshToken,
		}, nil
	})

	var wg sync.WaitGroup
	tokens := []string{"token-a", "token-b", "token-c"}
	results := make([]*TokenResult, len(tokens))

	for i, tok := range tokens {
		wg.Add(1)
		go func(idx int, rt string) {
			defer wg.Done()
			res, err := Refresh(context.Background(), &Params{
				Provider:     testProvider,
				RefreshToken: rt,
			})
			if err != nil {
				t.Errorf("token %s failed: %v", rt, err)
			}
			results[idx] = res
		}(i, tok)
	}

	wg.Wait()

	if got := callCount.Load(); got != int32(len(tokens)) {
		t.Fatalf("expected refresher to be called %d times for different tokens, got %d", len(tokens), got)
	}

	for i, tok := range tokens {
		if results[i] == nil || results[i].AccessToken != "token-"+tok {
			t.Errorf("unexpected result for token %s: %+v", tok, results[i])
		}
	}
}

func TestRefresh_DefaultClientUsedWhenNil(t *testing.T) {
	testProvider := "test-default-client"
	var capturedClient *http.Client

	Register(testProvider, func(ctx context.Context, p *Params) (*TokenResult, error) {
		capturedClient = p.Client
		return &TokenResult{AccessToken: "ok"}, nil
	})

	res, err := Refresh(context.Background(), &Params{
		Provider:     testProvider,
		RefreshToken: "some-rt",
		Client:       nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.AccessToken != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if capturedClient != DefaultOAuthClient {
		t.Errorf("expected capturedClient to be DefaultOAuthClient, got %v", capturedClient)
	}
}

func TestRefresh_CustomClientPreserved(t *testing.T) {
	testProvider := "test-custom-client"
	customClient := &http.Client{Timeout: 5 * time.Second}
	var capturedClient *http.Client

	Register(testProvider, func(ctx context.Context, p *Params) (*TokenResult, error) {
		capturedClient = p.Client
		return &TokenResult{AccessToken: "ok"}, nil
	})

	_, err := Refresh(context.Background(), &Params{
		Provider:     testProvider,
		RefreshToken: "custom-rt",
		Client:       customClient,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedClient != customClient {
		t.Errorf("expected customClient to be preserved, got %v", capturedClient)
	}
}

func TestRefresh_ErrorPropagation(t *testing.T) {
	testProvider := "test-error-propagation"
	var callCount atomic.Int32
	refreshErr := errors.New("upstream oauth token endpoint failure")

	Register(testProvider, func(ctx context.Context, p *Params) (*TokenResult, error) {
		callCount.Add(1)
		time.Sleep(30 * time.Millisecond)
		return nil, refreshErr
	})

	const concurrentCalls = 5
	var wg sync.WaitGroup
	errs := make([]error, concurrentCalls)

	for i := 0; i < concurrentCalls; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := Refresh(context.Background(), &Params{
				Provider:     testProvider,
				RefreshToken: "error-rt",
			})
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	if got := callCount.Load(); got != 1 {
		t.Fatalf("expected 1 call on singleflight error, got %d", got)
	}
	for i, err := range errs {
		if !errors.Is(err, refreshErr) && (err == nil || err.Error() != refreshErr.Error()) {
			t.Errorf("call %d expected %v, got %v", i, refreshErr, err)
		}
	}
}

func TestRefresh_PreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Refresh(ctx, &Params{
		Provider:     "any",
		RefreshToken: "rt",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestRefresh_NilParamsAndUnknownProvider(t *testing.T) {
	if _, err := Refresh(context.Background(), nil); err == nil {
		t.Errorf("expected error for nil params")
	}

	if _, err := Refresh(context.Background(), &Params{Provider: "unknown-provider-xyz"}); err == nil {
		t.Errorf("expected error for unregistered provider")
	}
}
