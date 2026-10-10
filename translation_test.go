package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContainsHan(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "simplified Chinese", text: "人工智能新闻", want: true},
		{name: "traditional Chinese", text: "人工智慧", want: true},
		{name: "English only", text: "Anthropic launches a model", want: false},
		{name: "empty", text: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsHan(tt.text); got != tt.want {
				t.Fatalf("containsHan(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestHandleTranslateRejectsGet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/translate", nil)
	rec := httptest.NewRecorder()
	handleTranslate(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleTranslateRejectsMoreThanThirtyTexts(t *testing.T) {
	texts := make([]string, 31)
	for i := range texts {
		texts[i] = "test"
	}
	body := []byte(`{"texts":[` + strings.TrimSuffix(strings.Repeat(`"test",`, 31), ",") + `]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/translate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleTranslate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
