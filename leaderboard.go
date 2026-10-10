package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

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
}{Results: make(map[string]LeaderboardResult)}

// Track refreshes per category. One slow Hugging Face category should not block
// other tabs from loading cached or independent leaderboard data.
var leaderboardRefreshing = struct {
	sync.Mutex
	items map[string]bool
}{items: make(map[string]bool)}

func startLeaderboardScheduler() {
	go func() {
		// Warm only the default tab at startup; eager fan-out across every HF config
		// commonly triggers HTTP 429 and makes the entire page appear broken.
		refreshLeaderboards("agents")
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			for _, category := range leaderboardCategories {
				refreshLeaderboards(category.ID)
				// Keep requests spaced out to avoid Hugging Face rate limiting.
				time.Sleep(5 * time.Second)
			}
		}
	}()
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
	var category LeaderboardCategory
	found := false
	for _, c := range leaderboardCategories {
		if c.ID == id {
			category, found = c, true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "未知排行榜分类", "categories": leaderboardCategories})
		return
	}
	leaderboardState.RLock()
	result, cached := leaderboardState.Results[id]
	lastAttempt := leaderboardState.LastAttempt
	leaderboardState.RUnlock()
	leaderboardRefreshing.Lock()
	busy := leaderboardRefreshing.items[id]
	leaderboardRefreshing.Unlock()
	force := r.URL.Query().Get("refresh") == "1"
	needsRefresh := (!cached && time.Since(lastAttempt) > 30*time.Second) || (cached && time.Since(result.FetchedAt) > 24*time.Hour)
	if (force || needsRefresh) && !busy {
		go refreshLeaderboards(id)
	}
	leaderboardState.RLock()
	result, cached = leaderboardState.Results[id]
	leaderboardState.RUnlock()
	leaderboardRefreshing.Lock()
	busy = leaderboardRefreshing.items[id]
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

func refreshLeaderboards(categoryID string) {
	var category LeaderboardCategory
	found := false
	for _, candidate := range leaderboardCategories {
		if candidate.ID == categoryID {
			category, found = candidate, true
			break
		}
	}
	if !found {
		return
	}
	leaderboardRefreshing.Lock()
	if leaderboardRefreshing.items[categoryID] {
		leaderboardRefreshing.Unlock()
		return
	}
	leaderboardRefreshing.items[categoryID] = true
	leaderboardRefreshing.Unlock()

	leaderboardState.Lock()
	leaderboardState.LastAttempt = time.Now()
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
	leaderboardState.Unlock()

	leaderboardRefreshing.Lock()
	delete(leaderboardRefreshing.items, categoryID)
	leaderboardRefreshing.Unlock()
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
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		} else {
			lastErr = fmt.Errorf("Hugging Face 返回 HTTP %d", resp.StatusCode)
			delay := time.Duration(2<<attempt) * time.Second
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
			time.Sleep(time.Duration(1<<attempt) * time.Second)
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
	req, err := http.NewRequest(http.MethodGet, "https://huggingface.co/api/models?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AI-Radar/1.3")
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
	req, err := http.NewRequest(http.MethodGet, "https://datasets-server.huggingface.co/rows?"+values.Encode(), nil)
	if err != nil {
		return LeaderboardResult{}, err
	}
	req.Header.Set("User-Agent", "AI-Radar/1.2")
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
