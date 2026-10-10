package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubLeaderboardUpstream mimics the parts of the Hugging Face API the leaderboard
// uses: datasets-server rows for LMArena configs and the models list for the
// community (music / 3D) categories.
func stubLeaderboardUpstream(t *testing.T, delay time.Duration, calls *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			atomic.AddInt32(calls, 1)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/api/models") {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "org/model-a", "author": "org", "downloads": 900, "likes": 30, "lastModified": "2026-01-02T00:00:00.000Z"},
				{"id": "org/model-b", "author": "org", "downloads": 500, "likes": 10, "lastModified": "2026-01-01T00:00:00.000Z"},
			})
			return
		}
		rows := make([]map[string]any, 0, 3)
		for i := 1; i <= 3; i++ {
			rows = append(rows, map[string]any{"row": map[string]any{
				"model_name": "model-" + string(rune('a'+i-1)), "organization": "org", "license": "mit",
				"rating": 1400 - float64(i), "score": 0.5, "rating_lower": 1390, "rating_upper": 1410,
				"vote_count": 1000, "rank": i, "category": "agent", "leaderboard_publish_date": "2026-01-02",
			}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows})
	}))
	t.Cleanup(server.Close)
	return server
}

func useStubEndpoints(t *testing.T, server *httptest.Server) {
	t.Helper()
	rows, models := leaderboardRowsEndpoint, leaderboardModelsEndpoint
	leaderboardRowsEndpoint, leaderboardModelsEndpoint = server.URL, server.URL+"/api/models"
	t.Cleanup(func() { leaderboardRowsEndpoint, leaderboardModelsEndpoint = rows, models })
}

// isolateLeaderboardState points the on-disk cache at a temp dir and clears memory.
func isolateLeaderboardState(t *testing.T) {
	t.Helper()
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	leaderboardState.Lock()
	leaderboardState.Results = make(map[string]LeaderboardResult)
	leaderboardState.Attempts = make(map[string]time.Time)
	leaderboardState.LastAttempt = time.Time{}
	leaderboardState.Unlock()
	leaderboardRefreshing.Lock()
	leaderboardRefreshing.items = make(map[string]bool)
	leaderboardRefreshing.done = make(map[string]chan struct{})
	leaderboardRefreshing.Unlock()
}

func leaderboardRequest(t *testing.T, target string) (map[string]any, time.Duration) {
	t.Helper()
	recorder := httptest.NewRecorder()
	started := time.Now()
	handleLeaderboard(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	elapsed := time.Since(started)
	if recorder.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, body = %s", target, recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("%s: invalid JSON: %v", target, err)
	}
	return payload, elapsed
}

// The first visit to a cold category must return real rows instead of an empty
// table that only fills in after the browser's slow 60s poll.
func TestLeaderboardFirstRequestWaitsForRows(t *testing.T) {
	isolateLeaderboardState(t)
	upstream := stubLeaderboardUpstream(t, 1200*time.Millisecond, nil)
	useStubEndpoints(t, upstream)

	payload, elapsed := leaderboardRequest(t, "/api/leaderboard?category=agents")
	items, _ := payload["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("first response items = %d, want 3 (payload: %v)", len(items), payload)
	}
	if loading, _ := payload["loading"].(bool); loading {
		t.Errorf("first response still reports loading=true after rows were fetched")
	}
	if elapsed < time.Second {
		t.Errorf("response in %v: the handler did not wait for the upstream fetch", elapsed)
	}
	if elapsed > 15*time.Second {
		t.Errorf("response took %v, too slow for a first page load", elapsed)
	}
	// A second request must be served from memory without touching the upstream.
	before := time.Now()
	payload, _ = leaderboardRequest(t, "/api/leaderboard?category=agents")
	if since := time.Since(before); since > 500*time.Millisecond {
		t.Errorf("cached response took %v, want < 500ms", since)
	}
	if items, _ := payload["items"].([]any); len(items) != 3 {
		t.Errorf("cached response items = %d, want 3", len(items))
	}
}

// Concurrent first requests for one category must share a single upstream call.
func TestLeaderboardRefreshIsSingleFlight(t *testing.T) {
	isolateLeaderboardState(t)
	var calls int32
	upstream := stubLeaderboardUpstream(t, 300*time.Millisecond, &calls)
	useStubEndpoints(t, upstream)

	done := make(chan struct{})
	for i := 0; i < 5; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			leaderboardRequest(t, "/api/leaderboard?category=agents")
		}()
	}
	for i := 0; i < 5; i++ {
		<-done
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("upstream calls = %d, want 1 single-flight fetch", got)
	}
}

// A fresh snapshot must be served without any upstream traffic.
func TestLeaderboardServesFreshSnapshotOffline(t *testing.T) {
	isolateLeaderboardState(t)
	var calls int32
	upstream := stubLeaderboardUpstream(t, 0, &calls)
	useStubEndpoints(t, upstream)

	leaderboardState.Lock()
	leaderboardState.Results["agents"] = LeaderboardResult{
		Category:  LeaderboardCategory{ID: "agents", Name: "智能体"},
		Items:     []LeaderboardItem{{ModelName: "cached-model", Rank: 1}},
		FetchedAt: time.Now(),
	}
	leaderboardState.Unlock()

	payload, elapsed := leaderboardRequest(t, "/api/leaderboard?category=agents")
	if elapsed > 500*time.Millisecond {
		t.Errorf("cached response took %v, want < 500ms", elapsed)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("upstream calls = %d, want 0 for a fresh snapshot", got)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %d, want the cached row", len(items))
	}
}

// Warm-up must leave every tab with usable data so switching categories is instant.
func TestWarmLeaderboardsFillsEveryCategory(t *testing.T) {
	isolateLeaderboardState(t)
	upstream := stubLeaderboardUpstream(t, 0, nil)
	useStubEndpoints(t, upstream)

	warmLeaderboards()

	leaderboardState.RLock()
	defer leaderboardState.RUnlock()
	for _, category := range leaderboardCategories {
		result, ok := leaderboardState.Results[category.ID]
		if !ok || len(result.Items) == 0 {
			t.Fatalf("category %s was not warmed (ok=%v, items=%d)", category.ID, ok, len(result.Items))
		}
		if result.Error != "" {
			t.Fatalf("category %s warmed with error: %s", category.ID, result.Error)
		}
	}
}

// The disk snapshot must survive a restart so the first paint after launch is instant.
func TestLeaderboardDiskCacheRoundTrip(t *testing.T) {
	isolateLeaderboardState(t)
	leaderboardState.Lock()
	leaderboardState.Results["agents"] = LeaderboardResult{
		Category:  LeaderboardCategory{ID: "agents", Name: "智能体"},
		Items:     []LeaderboardItem{{ModelName: "disk-model", Rank: 1}},
		FetchedAt: time.Now(),
	}
	saveLeaderboardCache()
	leaderboardState.Results = make(map[string]LeaderboardResult)
	leaderboardState.Unlock()

	loadLeaderboardCache()

	leaderboardState.RLock()
	defer leaderboardState.RUnlock()
	result, ok := leaderboardState.Results["agents"]
	if !ok || len(result.Items) != 1 || result.Items[0].ModelName != "disk-model" {
		t.Fatalf("disk cache did not round-trip: ok=%v result=%+v", ok, result)
	}
}

func TestLeaderboardRejectsUnknownCategory(t *testing.T) {
	isolateLeaderboardState(t)
	recorder := httptest.NewRecorder()
	handleLeaderboard(recorder, httptest.NewRequest(http.MethodGet, "/api/leaderboard?category=nope", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// A failing upstream must be bounded in time and cooled down per category, so one
// broken tab cannot block or hammer the others.
func TestLeaderboardFailureCooldownIsPerCategory(t *testing.T) {
	isolateLeaderboardState(t)
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "upstream down", http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	useStubEndpoints(t, upstream)

	payload, elapsed := leaderboardRequest(t, "/api/leaderboard?category=agents")
	if elapsed > 11*time.Second {
		t.Errorf("failed first load took %v, want the handler to give up quickly", elapsed)
	}
	if message, _ := payload["error"].(string); message == "" {
		t.Errorf("expected an error in the payload, got %v", payload)
	}
	afterFirst := atomic.LoadInt32(&calls)
	if afterFirst == 0 {
		t.Fatal("no upstream attempt was made")
	}

	leaderboardRequest(t, "/api/leaderboard?category=agents")
	if got := atomic.LoadInt32(&calls); got != afterFirst {
		t.Errorf("cooldown did not hold for the failing category: calls %d -> %d", afterFirst, got)
	}

	leaderboardRequest(t, "/api/leaderboard?category=coding")
	if got := atomic.LoadInt32(&calls); got == afterFirst {
		t.Errorf("another category was blocked by the failing category's cooldown")
	}
}
