package notify

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBusinessFailureAtHTTP200IsRetried(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			fmt.Fprint(w, `{"errcode":45009,"errmsg":"rate limited"}`)
			return
		}
		fmt.Fprint(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer ts.Close()
	m, err := NewManager(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.postWebhook(ts.URL, map[string]string{"text": "QA"})
	if got := attempts.Load(); got != 2 {
		t.Fatalf("got %d attempts, want business-failure retry", got)
	}
}

func TestBusinessFailureIsReturnedToTestNotification(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errcode":40014,"errmsg":"invalid token"}`)
	}))
	defer ts.Close()
	m, err := NewManager(filepath.Join(t.TempDir(), "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.postWebhookSync(ts.URL, nil); err == nil || !strings.Contains(err.Error(), "40014") {
		t.Fatalf("expected business error, got %v", err)
	}
}
