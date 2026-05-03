package extractor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── Registry tests ──────────────────────────────────────────────────────────

func TestRegister_Dispatch(t *testing.T) {
	// Save and restore registry state around this test.
	mu.Lock()
	saved := registry
	registry = nil
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = saved
		mu.Unlock()
	}()

	Register(`^https://example\.com/`, func(rawURL string, p ClientParams) Extractor {
		return &stubExtractor{name: "example"}
	})
	Register(`^https://other\.com/`, func(rawURL string, p ClientParams) Extractor {
		return &stubExtractor{name: "other"}
	})

	params := ClientParams{HTTP: http.DefaultClient}

	ex, ok := Dispatch("https://example.com/user/123", params)
	if !ok {
		t.Fatal("expected match for example.com")
	}
	if ex.Name() != "example" {
		t.Errorf("name: got %q, want %q", ex.Name(), "example")
	}

	ex2, ok2 := Dispatch("https://other.com/page", params)
	if !ok2 {
		t.Fatal("expected match for other.com")
	}
	if ex2.Name() != "other" {
		t.Errorf("name: got %q, want %q", ex2.Name(), "other")
	}

	_, ok3 := Dispatch("https://nomatch.com/", params)
	if ok3 {
		t.Error("expected no match for nomatch.com")
	}
}

func TestRegister_FirstMatchWins(t *testing.T) {
	mu.Lock()
	saved := registry
	registry = nil
	mu.Unlock()
	defer func() {
		mu.Lock()
		registry = saved
		mu.Unlock()
	}()

	Register(`^https://example\.com/`, func(_ string, _ ClientParams) Extractor {
		return &stubExtractor{name: "first"}
	})
	Register(`^https://example\.com/special/`, func(_ string, _ ClientParams) Extractor {
		return &stubExtractor{name: "second"}
	})

	params := ClientParams{HTTP: http.DefaultClient}
	ex, ok := Dispatch("https://example.com/special/abc", params)
	if !ok {
		t.Fatal("expected match")
	}
	// First registration wins.
	if ex.Name() != "first" {
		t.Errorf("got %q, want %q", ex.Name(), "first")
	}
}

// ─── Retry logic tests ───────────────────────────────────────────────────────

func TestRetryGet_SucceedsAfterTransient500(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := srv.Client()
	resp, err := retryGet(context.Background(), client, srv.URL+"/", nil, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if calls < 3 {
		t.Errorf("expected at least 3 calls, got %d", calls)
	}
}

func TestRetryGet_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := srv.Client()
	_, err := retryGet(context.Background(), client, srv.URL+"/", nil, 2)
	if err == nil {
		t.Fatal("expected an error after max retries")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryGet_CancellationStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := srv.Client()
	_, err := retryGet(ctx, client, srv.URL+"/", nil, 4)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

// ─── Paginator tests ─────────────────────────────────────────────────────────

func TestPaginate_DrainsTwoPages(t *testing.T) {
	pages := [][]int{{1, 2, 3}, {4, 5}}
	cursors := []string{"page2", ""}
	var idx int
	var mu sync.Mutex

	ch := Paginate(context.Background(), func(_ context.Context, cursor string) ([]int, string, error) {
		mu.Lock()
		i := idx
		idx++
		mu.Unlock()
		if i >= len(pages) {
			return nil, "", nil
		}
		return pages[i], cursors[i], nil
	})

	var got []int
	for v := range ch {
		got = append(got, v)
	}
	want := []int{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("[%d]: got %d, want %d", i, got[i], v)
		}
	}
}

func TestPaginate_StopsOnCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	ch := Paginate(ctx, func(ctx context.Context, _ string) ([]int, string, error) {
		return []int{1, 2, 3}, "more", nil // infinite pages
	})

	// Read one item then cancel.
	<-ch
	cancel()

	// Drain remaining (channel should close promptly).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed - pass
			}
		case <-deadline:
			t.Fatal("paginator did not stop after ctx cancellation")
		}
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

type stubExtractor struct {
	name string
}

func (s *stubExtractor) Name() string     { return s.name }
func (s *stubExtractor) Category() string { return "test" }
func (s *stubExtractor) Items(_ context.Context) <-chan Item {
	ch := make(chan Item)
	close(ch)
	return ch
}
