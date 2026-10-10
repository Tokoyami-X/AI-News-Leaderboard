package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

type translateRequest struct {
	Texts []string `json:"texts"`
}
type translateResponse struct {
	Translations []string `json:"translations"`
	Translated   int      `json:"translated"`
	Failed       int      `json:"failed"`
}

var translationCache = struct {
	sync.RWMutex
	values map[string]string
}{values: make(map[string]string)}

var translationClient = &http.Client{Timeout: 8 * time.Second}

func handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "只支持 POST 请求", http.StatusMethodNotAllowed)
		return
	}
	var input translateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&input); err != nil {
		http.Error(w, "请求格式无效", http.StatusBadRequest)
		return
	}
	if len(input.Texts) > 30 {
		http.Error(w, "每次最多翻译 30 段文字", http.StatusBadRequest)
		return
	}
	output := make([]string, len(input.Texts))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	var mu sync.Mutex
	translated, failed := 0, 0
	for i, text := range input.Texts {
		i, text := i, strings.TrimSpace(text)
		if text == "" {
			continue
		}
		translationCache.RLock()
		cached, ok := translationCache.values[text]
		translationCache.RUnlock()
		if ok {
			output[i] = cached
			translated++
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result, err := translateToChinese(text)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				output[i] = text
				failed++
				return
			}
			output[i] = result
			translationCache.Lock()
			translationCache.values[text] = result
			translationCache.Unlock()
			translated++
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, translateResponse{Translations: output, Translated: translated, Failed: failed})
}

func translateToChinese(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if len(text) > 1800 {
		text = text[:1800]
	}
	// PowerShell uses the Windows system proxy, which Go's default client does not read.
	if runtime.GOOS == "windows" {
		if result, err := translateViaPowerShell(text); err == nil && (containsHan(result) || containsHan(text)) {
			return result, nil
		}
	}
	// Prefer Google first: the no-key MyMemory endpoint may throttle requests with HTTP 429.
	googleURL := "https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=zh-CN&dt=t&q=" + url.QueryEscape(text)
	googleReq, err := http.NewRequest(http.MethodGet, googleURL, nil)
	if err == nil {
		googleReq.Header.Set("User-Agent", "Mozilla/5.0 AI-Radar/1.2")
		googleResp, requestErr := translationClient.Do(googleReq)
		if requestErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(googleResp.Body, 1<<20))
			googleResp.Body.Close()
			if readErr == nil && googleResp.StatusCode >= 200 && googleResp.StatusCode < 300 {
				var payload []any
				if json.Unmarshal(body, &payload) == nil && len(payload) > 0 {
					if segments, ok := payload[0].([]any); ok {
						var out strings.Builder
						for _, segment := range segments {
							pair, ok := segment.([]any)
							if ok && len(pair) > 0 {
								if s, ok := pair[0].(string); ok {
									out.WriteString(s)
								}
							}
						}
						result := strings.TrimSpace(out.String())
						if result != "" && (containsHan(result) || containsHan(text)) {
							return result, nil
						}
						if result != "" && result != text {
							// If the source was English, reject a response that stayed entirely English.
							googleErr := fmt.Errorf("Google 翻译返回的内容没有中文")
							_ = googleErr
						}
					}
				}
			}
		}
	}
	// MyMemory is a secondary fallback; validate its output instead of accepting unchanged English.
	memoryURL := "https://api.mymemory.translated.net/get?q=" + url.QueryEscape(text) + "&langpair=en|zh-CN"
	memoryReq, err := http.NewRequest(http.MethodGet, memoryURL, nil)
	if err == nil {
		memoryReq.Header.Set("User-Agent", "Mozilla/5.0 AI-Radar/1.2")
		memoryResp, requestErr := translationClient.Do(memoryReq)
		if requestErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(memoryResp.Body, 1<<20))
			memoryResp.Body.Close()
			if readErr == nil && memoryResp.StatusCode >= 200 && memoryResp.StatusCode < 300 {
				var payload struct {
					ResponseData struct {
						TranslatedText string `json:"translatedText"`
					} `json:"responseData"`
					ResponseStatus int `json:"responseStatus"`
				}
				if json.Unmarshal(body, &payload) == nil && payload.ResponseStatus == 200 {
					result := strings.TrimSpace(payload.ResponseData.TranslatedText)
					if result != "" && (containsHan(result) || containsHan(text)) {
						return result, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("Google 和 MyMemory 翻译服务均未返回有效中文结果")
}

func translateViaPowerShell(text string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	script := "$ErrorActionPreference='Stop'; $ProgressPreference='SilentlyContinue'; [Console]::OutputEncoding=New-Object System.Text.UTF8Encoding; $q=[uri]::EscapeDataString($env:AI_RADAR_TRANSLATE_TEXT); $u='https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=zh-CN&dt=t&q='+$q; $r=Invoke-RestMethod -Uri $u -TimeoutSec 12 -Headers @{'User-Agent'='Mozilla/5.0'}; $parts=@(); foreach($seg in $r[0]) { if($seg -is [array] -and $seg.Count -gt 0) { $parts += [string]$seg[0] } }; [Console]::Write(($parts -join ''))"
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Env = append(os.Environ(), "AI_RADAR_TRANSLATE_TEXT="+text)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	result := strings.TrimSpace(string(output))
	if result == "" {
		return "", fmt.Errorf("PowerShell 翻译结果为空")
	}
	return result, nil
}

func containsHan(s string) bool {
	for _, r := range s {
		if (r >= 0x3400 && r <= 0x4DBF) || (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0xF900 && r <= 0xFAFF) {
			return true
		}
	}
	return false
}
