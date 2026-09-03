package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"kairo/internal/schedtask"
)

func TestNotifyManagerUpdateAndMasking(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "task-notifications.json")

	m, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	view := m.View()
	if !view.Enabled || !view.WindowsToast {
		t.Fatalf("Default config should be enabled with WindowsToast=true, got %+v", view)
	}
	if view.WeComConfigured || view.DingConfigured {
		t.Fatalf("Default config should not have webhooks configured")
	}

	// Invalid webhook URL
	err = m.Update(Config{
		Enabled:      true,
		WindowsToast: true,
		WeComWebhook: "not-a-url",
	})
	if err == nil {
		t.Fatalf("Expected error for invalid WeCom webhook URL")
	}

	// Valid update
	err = m.Update(Config{
		Enabled:      true,
		WindowsToast: true,
		WeComWebhook: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key-123",
		DingWebhook:  "https://oapi.dingtalk.com/robot/send?access_token=token-456",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// View should mask the URLs and only indicate configured=true
	view = m.View()
	if !view.WeComConfigured || !view.DingConfigured {
		t.Fatalf("Expected both webhooks configured, got %+v", view)
	}

	// File should be persisted
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("Failed to read persisted config: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("Failed to unmarshal persisted config: %v", err)
	}
	if persisted.WeComWebhook != "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret-key-123" {
		t.Fatalf("Persisted WeCom webhook mismatch")
	}

	// UpdatePatch preserves secrets when not supplied
	newEnabled := false
	err = m.UpdatePatch(&newEnabled, nil, nil, nil)
	if err != nil {
		t.Fatalf("UpdatePatch failed: %v", err)
	}
	if m.View().Enabled {
		t.Fatalf("Enabled should be false after patch")
	}
	if !m.View().WeComConfigured || !m.View().DingConfigured {
		t.Fatalf("UpdatePatch should preserve existing webhooks when nil")
	}
}

func TestNotifyFailureDeliveryAndDedup(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "task-notifications.json")

	var wecomCalls atomic.Int32
	var dingCalls atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wecom" {
			wecomCalls.Add(1)
		} else if r.URL.Path == "/ding" {
			dingCalls.Add(1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer ts.Close()

	m, err := NewManager(cfgPath)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	err = m.Update(Config{
		Enabled:      true,
		WindowsToast: false, // avoid popup in tests
		WeComWebhook: ts.URL + "/wecom",
		DingWebhook:  ts.URL + "/ding",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	event := schedtask.FailureEvent{
		TaskID:     "task-01",
		TaskName:   "测试定时同步",
		RunID:      "run-001",
		Status:     "failed",
		Error:      "exit code 1: network timeout",
		StartedAt:  time.Now().Format(time.RFC3339),
		FinishedAt: time.Now().Format(time.RFC3339),
	}

	m.NotifyFailure(event)
	// Same event delivered again should be deduplicated
	m.NotifyFailure(event)

	time.Sleep(200 * time.Millisecond)

	if wecomCalls.Load() != 1 {
		t.Fatalf("Expected exactly 1 WeCom call due to dedup, got %d", wecomCalls.Load())
	}
	if dingCalls.Load() != 1 {
		t.Fatalf("Expected exactly 1 DingTalk call due to dedup, got %d", dingCalls.Load())
	}
}
