package main

import (
	"embed"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type Source struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Feed     string `json:"feed"`
	Fallback string `json:"fallback,omitempty"`
}

// Google News RSS keeps China-focused categories current without inventing articles.
func googleNewsFeed(query string) string {
	return "https://news.google.com/rss/search?q=" + url.QueryEscape(query) + "&hl=zh-CN&gl=CN&ceid=CN:zh"
}

var sources = []Source{{"openai", "OpenAI", "https://openai.com/news/", "https://openai.com/news/rss.xml", "https://news.google.com/rss/search?q=site%3Aopenai.com%2Findex%2Fnews&hl=en-US&gl=US&ceid=US%3Aen"},
	{"google", "Google AI", "https://blog.google/technology/google-labs/", "https://blog.google/technology/google-labs/rss/", "https://news.google.com/rss/search?q=site%3Ablog.google%2Ftechnology%2Fgoogle-labs&hl=en-US&gl=US&ceid=US%3Aen"},
	{"anthropic", "Anthropic", "https://www.anthropic.com/news", "https://raw.githubusercontent.com/Olshansk/rss-feeds/main/feeds/feed_anthropic_news.xml", "https://news.google.com/rss/search?q=site%3Aanthropic.com%2Fnews&hl=en-US&gl=US&ceid=US%3Aen"},
	{"huggingface", "Hugging Face", "https://huggingface.co/blog", "https://huggingface.co/blog/feed.xml", "https://news.google.com/rss/search?q=site%3Ahuggingface.co%2Fblog&hl=en-US&gl=US&ceid=US%3Aen"},
	{"chinaai", "中国 AI 综合", "https://news.google.com/search?q=人工智能+中国&hl=zh-CN", googleNewsFeed("中国 人工智能 AI 科技"), "https://www.qbitai.com/feed"},
	{"chinamodels", "中国大模型/产品", "https://news.google.com/search?q=中国+大模型+DeepSeek+通义千问&hl=zh-CN", googleNewsFeed("DeepSeek OR 通义千问 OR 豆包 OR 文心一言 OR 智谱 AI 大模型"), "https://www.infoq.cn/feed"},
	{"chinastartups", "中国 AI 公司", "https://news.google.com/search?q=中国+AI+公司&hl=zh-CN", googleNewsFeed("中国 AI 人工智能 公司 创业 融资"), "https://www.qbitai.com/feed"},
	{"chinapolicy", "中国 AI 政策/产业", "https://news.google.com/search?q=中国+人工智能+政策&hl=zh-CN", googleNewsFeed("中国 人工智能 AI 政策 产业 监管"), "https://www.infoq.cn/feed"},
	{"chinaembodied", "机器人/具身智能", "https://news.google.com/search?q=中国+具身智能+机器人&hl=zh-CN", googleNewsFeed("中国 具身智能 人形机器人 AI 机器人"), "https://www.qbitai.com/feed"},
}

type NewsItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	URL         string    `json:"url"`
	SourceID    string    `json:"sourceId"`
	Source      string    `json:"source"`
	PublishedAt time.Time `json:"publishedAt"`
}
type rssFeed struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			PubDate     string `xml:"pubDate"`
			GUID        string `xml:"guid"`
		} `xml:"item"`
	} `xml:"channel"`
	Entries []struct {
		Title string `xml:"title"`
		Link  struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
		Summary   string `xml:"summary"`
		Content   string `xml:"content"`
		ID        string `xml:"id"`
		Updated   string `xml:"updated"`
		Published string `xml:"published"`
	} `xml:"entry"`
}
type cacheState struct {
	mu         sync.RWMutex
	items      []NewsItem
	fetchedAt  time.Time
	errors     []string
	refreshing bool
}

var cache cacheState

//go:embed web
var embeddedWeb embed.FS

// Feed downloads run concurrently; allow larger feeds time to finish.
var client = &http.Client{Timeout: 18 * time.Second}

// openBrowser opens the page in Edge, falling back to the system default browser.
func openBrowser(page string) {
	if runtime.GOOS != "windows" {
		if runtime.GOOS == "darwin" {
			_ = exec.Command("open", page).Start()
		} else {
			_ = exec.Command("xdg-open", page).Start()
		}
		return
	}
	candidates := []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	}
	for _, edge := range candidates {
		if _, statErr := os.Stat(edge); statErr != nil {
			continue
		}
		if startErr := exec.Command(edge, "--new-window", page).Start(); startErr == nil {
			return
		} else {
			log.Printf("Edge 启动失败，改用默认浏览器: %v", startErr)
			break
		}
	}
	// Fall back to the shell's default browser handler.
	if err := exec.Command("cmd", "/c", "start", "", page).Start(); err != nil {
		log.Printf("打开浏览器失败: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/news", handleNews)
	mux.HandleFunc("/api/translate", handleTranslate)
	mux.HandleFunc("/api/leaderboard", handleLeaderboard)
	mux.HandleFunc("/api/sources", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"sources": sources}) })
	webFS, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(webFS)))
	addr := strings.TrimSpace(os.Getenv("AI_NEWS_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	// Double-clicking the exe must always bring up the page, even when an earlier
	// instance still holds the default port.
	autoOpen := addr == ":8080"
	ln, err := net.Listen("tcp", addr)
	if err != nil && addr == ":8080" {
		for p := 8081; p <= 8099; p++ {
			if candidate, e := net.Listen("tcp", fmt.Sprintf(":%d", p)); e == nil {
				ln, addr, err = candidate, fmt.Sprintf(":%d", p), nil
				break
			}
		}
	}
	if err != nil {
		log.Fatalf("无法监听 %s: %v", addr, err)
	}
	log.Printf("AI Radar ready: http://localhost%s", addr)
	// Loads the leaderboard snapshot from disk before serving, then warms the
	// categories in the background so opening the page is fast.
	startLeaderboardScheduler()
	if autoOpen {
		go func() {
			time.Sleep(800 * time.Millisecond)
			page := strings.TrimSpace(os.Getenv("AI_NEWS_START_URL"))
			if page == "" {
				page = "http://localhost" + addr + "/"
			}
			openBrowser(page)
		}()
	}
	log.Fatal(http.Serve(ln, mux))
}
func handleNews(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "只支持 GET 请求", 405)
		return
	}
	force := r.URL.Query().Get("refresh") == "1"
	cache.mu.RLock()
	stale := time.Since(cache.fetchedAt) > 5*time.Minute || len(cache.items) == 0
	busy := cache.refreshing
	cache.mu.RUnlock()
	if (force || stale) && !busy {
		// Refresh in the background: the API responds immediately, even when a provider is slow.
		go refreshFeeds()
	}
	cache.mu.RLock()
	items := append([]NewsItem(nil), cache.items...)
	fetched := cache.fetchedAt
	errs := append([]string(nil), cache.errors...)
	cache.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	src := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	out := make([]NewsItem, 0, len(items))
	for _, it := range items {
		if src != "" && src != "all" && src != it.SourceID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(it.Title+" "+it.Summary+" "+it.Source), q) {
			continue
		}
		out = append(out, it)
	}
	writeJSON(w, 200, map[string]any{"items": out, "count": len(out), "fetchedAt": fetched, "sources": sources, "errors": errs, "stale": !fetched.IsZero() && time.Since(fetched) > 10*time.Minute})
}
func refreshFeeds() {
	cache.mu.Lock()
	if cache.refreshing {
		cache.mu.Unlock()
		return
	}
	cache.refreshing = true
	cache.mu.Unlock()

	type result struct {
		items []NewsItem
		err   error
		name  string
	}
	results := make(chan result, len(sources))
	var wg sync.WaitGroup
	for _, source := range sources {
		s := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := fetchFeed(s)
			if err != nil && s.Fallback != "" {
				primaryErr := err
				fallback := s
				fallback.Feed = s.Fallback
				fallback.Fallback = ""
				items, err = fetchFeed(fallback)
				if err == nil {
					log.Printf("%s: primary feed failed; fallback feed succeeded", s.Name)
				} else {
					err = fmt.Errorf("主源失败: %v；备用源失败: %w", primaryErr, err)
				}
			}
			results <- result{items: items, err: err, name: s.Name}
		}()
	}
	go func() { wg.Wait(); close(results) }()

	var all []NewsItem
	var errs []string
	for res := range results {
		if res.err != nil {
			errs = append(errs, res.name+": "+res.err.Error())
			continue
		}
		all = append(all, res.items...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].PublishedAt.After(all[j].PublishedAt) })
	cache.mu.Lock()
	// Keep the previous successful cache if every provider fails.
	if len(all) > 0 || len(cache.items) == 0 {
		cache.items = all
		if len(all) > 0 {
			cache.fetchedAt = time.Now()
		}
	}
	cache.errors = errs
	cache.refreshing = false
	cache.mu.Unlock()
}
func fetchFeedViaPowerShell(feedURL string) ([]byte, error) {
	escapedURL := strings.ReplaceAll(feedURL, "'", "''")
	script := "$ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=[System.Text.Encoding]::UTF8; $r=Invoke-WebRequest -UseBasicParsing -TimeoutSec 20 -Uri '" + escapedURL + "'; [Console]::Out.Write($r.Content)"
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("PowerShell 下载失败: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("PowerShell 返回空内容")
	}
	return out, nil
}

func fetchFeed(s Source) ([]NewsItem, error) {
	req, err := http.NewRequest("GET", s.Feed, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; AI-Radar/1.1; +http://localhost:8080)")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml, */*")
	resp, err := client.Do(req)
	var body []byte
	if err != nil {
		// Windows PowerShell can use the system proxy configured by a VPN.
		if runtime.GOOS != "windows" {
			return nil, err
		}
		body, err = fetchFeedViaPowerShell(s.Feed)
		if err != nil {
			return nil, fmt.Errorf("Go 请求失败；系统代理请求失败: %w", err)
		}
	} else {
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("Feed 返回 HTTP %d", resp.StatusCode)
		}
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return nil, err
		}
	}
	var f rssFeed
	if err = xml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("解析 RSS/Atom 失败: %w", err)
	}
	var out []NewsItem
	for _, it := range f.Channel.Items {
		link := strings.TrimSpace(it.Link)
		if link == "" {
			link = strings.TrimSpace(it.GUID)
		}
		if link != "" && strings.TrimSpace(it.Title) != "" {
			out = append(out, NewsItem{stableID(s.ID, link), clean(it.Title), clean(it.Description), link, s.ID, s.Name, parseTime(it.PubDate)})
		}
	}
	for _, it := range f.Entries {
		link := strings.TrimSpace(it.Link.Href)
		if link == "" {
			link = strings.TrimSpace(it.ID)
		}
		if link != "" && strings.TrimSpace(it.Title) != "" {
			summary := it.Summary
			if summary == "" {
				summary = it.Content
			}
			date := it.Published
			if date == "" {
				date = it.Updated
			}
			out = append(out, NewsItem{stableID(s.ID, link), clean(it.Title), clean(summary), link, s.ID, s.Name, parseTime(date)})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Feed 中没有可识别的条目")
	}
	return out, nil
}
func clean(s string) string {
	s = strings.TrimSpace(s)
	for strings.Contains(s, "<") && strings.Contains(s, ">") {
		a := strings.Index(s, "<")
		b := strings.Index(s[a:], ">")
		if b < 0 {
			break
		}
		s = s[:a] + " " + s[a+b+1:]
	}
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&quot;", "\"", "&#39;", "'").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 420 {
		s = s[:420] + "…"
	}
	return s
}
func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822, "2006-01-02"} {
		if t, e := time.Parse(layout, strings.TrimSpace(s)); e == nil {
			return t
		}
	}
	return time.Time{}
}
func stableID(src, link string) string { return src + ":" + url.QueryEscape(link) }
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
