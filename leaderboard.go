package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoints are variables so tests can point them at a local stub server.
var (
	leaderboardRowsEndpoint   = "https://datasets-server.huggingface.co/rows"
	leaderboardModelsEndpoint = "https://huggingface.co/api/models"
)

// Leaderboard traffic uses its own client: a slow Hugging Face response must not
// block the news feeds, and short dial/TLS timeouts stop a VPN-routed connection
// from hanging before the retry logic can kick in.
var leaderboardClient = &http.Client{
	Timeout: 25 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 6 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   6 * time.Second,
		ExpectContinueTimeout: time.Second,
	},
}

type LeaderboardCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Config      string `json:"config"`
	Description string `json:"description"`
}

var leaderboardCategories = []LeaderboardCategory{
	{"agents", "智能体", "agent", "Agent Arena 任务表现"},
	{"text", "综合对话", "text_style_control", "日常问答与综合对话偏好"},
	{"coding", "编程能力", "webdev", "代码生成与网页开发"},
	{"vision", "视觉理解", "vision_style_control", "图像理解与视觉问答"},
	{"search", "联网搜索", "search", "搜索与信息检索"},
	{"document", "文档处理", "document_style_control", "长文档理解与处理"},
	{"text-factuality", "事实准确性", "text_factuality", "回答事实性与可靠性"},
	{"search-factuality", "搜索事实性", "search_factuality", "搜索结果的事实准确性"},
	{"text-to-image", "文生图", "text_to_image", "文本生成图像"},
	{"image-edit", "图像编辑", "image_edit", "图像编辑能力"},
	{"text-to-video", "文生视频", "text_to_video", "文本生成视频"},
	{"image-to-video", "图生视频", "image_to_video", "图像生成视频"},
	{"video-edit", "视频编辑", "video_edit", "视频编辑能力"},
	{"music", "音乐生成 AI", "hf:text-to-audio", "音乐与音频生成模型 · 按下载量排序"},
	{"three-d", "3D 模型生成", "hf:3d", "文本/图片生成 3D 资产 · 按下载量排序"},
}

type LeaderboardItem struct {
	ModelName    string  `json:"modelName"`
	Organization string  `json:"organization"`
	License      string  `json:"license"`
	Rating       float64 `json:"rating"`
	Score        float64 `json:"score,omitempty"`
	RatingLower  float64 `json:"ratingLower"`
	RatingUpper  float64 `json:"ratingUpper"`
	VoteCount    float64 `json:"voteCount"`
	Rank         int     `json:"rank"`
	Category     string  `json:"category"`
	PublishDate  string  `json:"publishDate"`
}

type LeaderboardResult struct {
	Items     []LeaderboardItem   `json:"items"`
	Category  LeaderboardCategory `json:"category"`
	UpdatedAt string              `json:"updatedAt"`
	FetchedAt time.Time           `json:"fetchedAt"`
	Error     string              `json:"error,omitempty"`
}

type leaderboardAPIResponse struct {
	Rows []struct {
		Row struct {
			ModelName    string  `json:"model_name"`
			Organization string  `json:"organization"`
			License      string  `json:"license"`
			Rating       float64 `json:"rating"`
			Score        float64 `json:"score"`
			RatingLower  float64 `json:"rating_lower"`
			RatingUpper  float64 `json:"rating_upper"`
			VoteCount    float64 `json:"vote_count"`
			Rank         int     `json:"rank"`
			Category     string  `json:"category"`
			PublishDate  string  `json:"leaderboard_publish_date"`
		} `json:"row"`
	} `json:"rows"`
}

var leaderboardState = struct {
	sync.RWMutex
	Results     map[string]LeaderboardResult
	Refreshing  bool
	LastAttempt time.Time
	// Attempts tracks the last fetch start per category so a failing upstream is
	// retried after a small cooldown instead of on every browser poll.
	Attempts map[string]time.Time
}{Results: make(map[string]LeaderboardResult), Attempts: make(map[string]time.Time)}

func leaderboardCacheFile() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "AI-Radar", "leaderboard-cache.json")
	}
	return "leaderboard-cache.json"
}

func loadLeaderboardCache() {
	data, err := os.ReadFile(leaderboardCacheFile())
	if err != nil {
		return
	}
	var results map[string]LeaderboardResult
	if json.Unmarshal(data, &results) == nil && results != nil {
		leaderboardState.Lock()
		leaderboardState.Results = results
		leaderboardState.Unlock()
	}
}

func saveLeaderboardCache() {
	data, err := json.MarshalIndent(leaderboardState.Results, "", "  ")
	if err != nil {
		return
	}
	path := leaderboardCacheFile()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, data, 0644)
}

func findLeaderboardCategory(id string) (LeaderboardCategory, bool) {
	for _, category := range leaderboardCategories {
		if category.ID == id {
			return category, true
		}
	}
	return LeaderboardCategory{}, false
}

// Track refreshes per category. One slow Hugging Face category should not block
// other tabs from loading cached or independent leaderboard data, and callers can
// wait on a running refresh through the done channel.
var leaderboardRefreshing = struct {
	sync.Mutex
	items map[string]bool
	done  map[string]chan struct{}
}{items: make(map[string]bool), done: make(map[string]chan struct{})}

// Start-up warm-up concurrency: enough to fill the tab bar quickly, low enough to
// stay clear of Hugging Face rate limiting.
const leaderboardWarmWorkers = 3

func startLeaderboardScheduler() {
	// Load the on-disk snapshot before the HTTP server starts answering requests.
	loadLeaderboardCache()
	go func() {
		warmLeaderboards()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			warmLeaderboards()
		}
	}()
}

// warmLeaderboards syncs every category in the background so switching tabs is
// instant instead of waiting for a cold Hugging Face round trip.
func warmLeaderboards() {
	sem := make(chan struct{}, leaderboardWarmWorkers)
	var wg sync.WaitGroup
	for _, category := range leaderboardCategories {
		sem <- struct{}{}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			wait, _ := startLeaderboardRefresh(id)
			if wait == nil {
				return
			}
			select {
			case <-wait:
			case <-time.After(45 * time.Second):
			}
		}(category.ID)
	}
	wg.Wait()
}

func handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET 请求", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("category"))
	if id == "" {
		id = "agents"
	}
	category, found := findLeaderboardCategory(id)
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知排行榜分类", "categories": leaderboardCategories})
		return
	}
	leaderboardState.RLock()
	result, cached := leaderboardState.Results[id]
	lastAttempt := leaderboardState.Attempts[id]
	leaderboardState.RUnlock()
	force := r.URL.Query().Get("refresh") == "1"
	stale := cached && time.Since(result.FetchedAt) > 24*time.Hour
	// A category that just failed should not be retried on every browser poll;
	// keep a small cooldown so a broken upstream cannot be hammered.
	cooling := !cached && time.Since(lastAttempt) < 10*time.Second
	if force || stale || (!cached && !cooling) {
		wait, _ := startLeaderboardRefresh(id)
		if !cached && wait != nil {
			// First visit for this category: wait for real rows instead of returning an
			// empty table that only fills up on the next poll.
			select {
			case <-wait:
			case <-time.After(12 * time.Second):
			case <-r.Context().Done():
			}
		}
	}
	leaderboardState.RLock()
	result, cached = leaderboardState.Results[id]
	leaderboardState.RUnlock()
	leaderboardRefreshing.Lock()
	busy := leaderboardRefreshing.items[id]
	leaderboardRefreshing.Unlock()
	if !cached {
		result = LeaderboardResult{Items: []LeaderboardItem{}, Category: category}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": result.Items, "category": category, "updatedAt": result.UpdatedAt,
		"fetchedAt": result.FetchedAt, "categories": leaderboardCategories,
		"refreshing": busy, "loading": !cached, "error": result.Error,
		"source": "LMArena 官方发布的 Hugging Face leaderboard-dataset",
	})
}

// startLeaderboardRefresh registers a refresh for one category and runs the fetch
// in the background. The returned channel is closed when the refresh finishes
// (nil when the category is unknown); started reports whether this call created it.
func startLeaderboardRefresh(categoryID string) (wait <-chan struct{}, started bool) {
	category, found := findLeaderboardCategory(categoryID)
	if !found {
		return nil, false
	}
	leaderboardRefreshing.Lock()
	if leaderboardRefreshing.items[categoryID] {
		ch := leaderboardRefreshing.done[categoryID]
		leaderboardRefreshing.Unlock()
		return ch, false
	}
	ch := make(chan struct{})
	leaderboardRefreshing.items[categoryID] = true
	leaderboardRefreshing.done[categoryID] = ch
	leaderboardRefreshing.Unlock()

	go func() {
		defer func() {
			leaderboardRefreshing.Lock()
			delete(leaderboardRefreshing.items, categoryID)
			delete(leaderboardRefreshing.done, categoryID)
			leaderboardRefreshing.Unlock()
			close(ch)
		}()
		runLeaderboardRefresh(category, categoryID)
	}()
	return ch, true
}

// refreshLeaderboards keeps the original fire-and-forget entry point.
func refreshLeaderboards(categoryID string) {
	_, _ = startLeaderboardRefresh(categoryID)
}

func runLeaderboardRefresh(category LeaderboardCategory, categoryID string) {
	leaderboardState.Lock()
	leaderboardState.LastAttempt = time.Now()
	leaderboardState.Attempts[categoryID] = leaderboardState.LastAttempt
	leaderboardState.Unlock()
	value, err := fetchLeaderboard(category)
	now := time.Now()
	leaderboardState.Lock()
	if err != nil {
		old, ok := leaderboardState.Results[categoryID]
		if ok {
			// Keep the last good snapshot visible even if the upstream is rate-limiting.
			old.Error = err.Error()
			leaderboardState.Results[categoryID] = old
		} else {
			leaderboardState.Results[categoryID] = LeaderboardResult{Category: category, Error: err.Error()}
		}
		log.Printf("Leaderboard %s refresh failed: %v", categoryID, err)
	} else {
		value.FetchedAt = now
		leaderboardState.Results[categoryID] = value
	}
	saveLeaderboardCache()
	leaderboardState.Unlock()
}

type hfModelEntry struct {
	ID           string  `json:"id"`
	Author       string  `json:"author"`
	Downloads    float64 `json:"downloads"`
	Likes        float64 `json:"likes"`
	LastModified string  `json:"lastModified"`
	PipelineTag  string  `json:"pipeline_tag"`
	CardData     struct {
		License string `json:"license"`
	} `json:"cardData"`
}

// doLeaderboardRequest retries temporary Hugging Face rate limits and server errors.
func doLeaderboardRequest(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := leaderboardClient.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		} else {
			lastErr = fmt.Errorf("Hugging Face 返回 HTTP %d", resp.StatusCode)
			delay := time.Duration(attempt+1) * time.Second
			if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
				if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds > 0 && seconds < 90 {
					delay = time.Duration(seconds) * time.Second
				}
			}
			resp.Body.Close()
			if attempt < 2 {
				time.Sleep(delay)
			}
			continue
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
	return nil, lastErr
}

func fetchHuggingFaceTask(task string) ([]hfModelEntry, error) {
	values := url.Values{}
	values.Set("pipeline_tag", task)
	values.Set("sort", "downloads")
	values.Set("direction", "-1")
	values.Set("limit", "100")
	req, err := http.NewRequest(http.MethodGet, leaderboardModelsEndpoint+"?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AI-Radar/1.3")
	req.Header.Set("Accept", "application/json")
	resp, err := doLeaderboardRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Hugging Face 模型 API 返回 HTTP %d", resp.StatusCode)
	}
	var models []hfModelEntry
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&models); err != nil {
		return nil, err
	}
	return models, nil
}

func fetchCommunityModelRanking(category LeaderboardCategory) (LeaderboardResult, error) {
	tasks := []string{"text-to-audio"}
	if category.ID == "three-d" {
		tasks = []string{"text-to-3d", "image-to-3d"}
	}
	seen := make(map[string]bool)
	models := make([]hfModelEntry, 0, 200)
	for _, task := range tasks {
		batch, err := fetchHuggingFaceTask(task)
		if err != nil {
			return LeaderboardResult{}, err
		}
		for _, model := range batch {
			if model.ID == "" || seen[model.ID] {
				continue
			}
			seen[model.ID] = true
			models = append(models, model)
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Downloads > models[j].Downloads })
	if len(models) == 0 {
		return LeaderboardResult{}, fmt.Errorf("Hugging Face 没有返回该分类的模型")
	}
	items := make([]LeaderboardItem, 0, len(models))
	publishDate := ""
	for i, model := range models {
		date := ""
		if len(model.LastModified) >= 10 {
			date = model.LastModified[:10]
		}
		if date > publishDate {
			publishDate = date
		}
		items = append(items, LeaderboardItem{ModelName: model.ID, Organization: model.Author, License: model.CardData.License, Rating: model.Downloads, VoteCount: float64(model.Likes), Rank: i + 1, Category: category.ID, PublishDate: date})
	}
	return LeaderboardResult{Items: items, Category: category, UpdatedAt: publishDate}, nil
}

func fetchLeaderboard(category LeaderboardCategory) (LeaderboardResult, error) {
	if strings.HasPrefix(category.Config, "hf:") {
		return fetchCommunityModelRanking(category)
	}
	values := url.Values{}
	values.Set("dataset", "lmarena-ai/leaderboard-dataset")
	values.Set("config", category.Config)
	values.Set("split", "latest")
	values.Set("offset", "0")
	values.Set("length", "100")
	req, err := http.NewRequest(http.MethodGet, leaderboardRowsEndpoint+"?"+values.Encode(), nil)
	if err != nil {
		return LeaderboardResult{}, err
	}
	req.Header.Set("User-Agent", "AI-Radar/1.2")
	req.Header.Set("Accept", "application/json")
	resp, err := doLeaderboardRequest(req)
	if err != nil {
		return LeaderboardResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return LeaderboardResult{}, fmt.Errorf("Hugging Face 返回 HTTP %d", resp.StatusCode)
	}
	var payload leaderboardAPIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload); err != nil {
		return LeaderboardResult{}, err
	}
	items := make([]LeaderboardItem, 0, len(payload.Rows))
	publishDate := ""
	for _, entry := range payload.Rows {
		row := entry.Row
		if strings.TrimSpace(row.ModelName) == "" {
			continue
		}
		items = append(items, LeaderboardItem{
			ModelName: row.ModelName, Organization: row.Organization, License: row.License,
			Rating: row.Rating, Score: row.Score, RatingLower: row.RatingLower,
			RatingUpper: row.RatingUpper, VoteCount: row.VoteCount, Rank: row.Rank,
			Category: row.Category, PublishDate: row.PublishDate,
		})
		if row.PublishDate > publishDate {
			publishDate = row.PublishDate
		}
	}
	if len(items) == 0 {
		return LeaderboardResult{}, fmt.Errorf("上游没有返回可用排行数据")
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Rank < items[j].Rank })
	return LeaderboardResult{Items: items, Category: category, UpdatedAt: publishDate}, nil
}
