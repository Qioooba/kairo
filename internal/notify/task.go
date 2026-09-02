// Package notify sends task failures to the local desktop and configured
// operations webhooks. Delivery is asynchronous and bounded by a small retry
// policy so it never blocks the scheduler.
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kairo/internal/popup"
	"kairo/internal/schedtask"
)

type Config struct {
	Enabled      bool   `json:"enabled"`
	WindowsToast bool   `json:"windows_toast"`
	WeComWebhook string `json:"wecom_webhook,omitempty"`
	DingWebhook  string `json:"dingtalk_webhook,omitempty"`
}

type View struct {
	Enabled         bool `json:"enabled"`
	WindowsToast    bool `json:"windows_toast"`
	WeComConfigured bool `json:"wecom_configured"`
	DingConfigured  bool `json:"dingtalk_configured"`
}

type Manager struct {
	path   string
	mu     sync.RWMutex
	cfg    Config
	client *http.Client
	seen   map[string]time.Time
}

func NewManager(path string) (*Manager, error) {
	m := &Manager{path: path, cfg: Config{Enabled: true, WindowsToast: true}, client: &http.Client{Timeout: 8 * time.Second}, seen: map[string]time.Time{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 64*1024 {
		return nil, fmt.Errorf("任务告警配置过大")
	}
	if err := json.Unmarshal(data, &m.cfg); err != nil {
		return nil, fmt.Errorf("解析任务告警配置失败: %w", err)
	}
	return m, nil
}

func (m *Manager) View() View {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return View{Enabled: m.cfg.Enabled, WindowsToast: m.cfg.WindowsToast, WeComConfigured: m.cfg.WeComWebhook != "", DingConfigured: m.cfg.DingWebhook != ""}
}

func (m *Manager) Update(cfg Config) error {
	if err := validateWebhook(cfg.WeComWebhook); err != nil {
		return fmt.Errorf("企业微信 Webhook: %w", err)
	}
	if err := validateWebhook(cfg.DingWebhook); err != nil {
		return fmt.Errorf("钉钉 Webhook: %w", err)
	}
	cfg.WeComWebhook = strings.TrimSpace(cfg.WeComWebhook)
	cfg.DingWebhook = strings.TrimSpace(cfg.DingWebhook)
	m.mu.Lock()
	old := m.cfg
	m.cfg = cfg
	snapshot := m.cfg
	m.mu.Unlock()
	if err := m.save(snapshot); err != nil {
		m.mu.Lock()
		m.cfg = old
		m.mu.Unlock()
		return err
	}
	return nil
}

// UpdatePatch preserves secrets when the HTTP client changes only switches.
// A nil webhook field means "keep the current value"; an explicit empty string
// clears that channel.
func (m *Manager) UpdatePatch(enabled, windows *bool, wecom, ding *string) error {
	m.mu.RLock(); cfg := m.cfg; m.mu.RUnlock()
	if enabled != nil { cfg.Enabled = *enabled }
	if windows != nil { cfg.WindowsToast = *windows }
	if wecom != nil { cfg.WeComWebhook = *wecom }
	if ding != nil { cfg.DingWebhook = *ding }
	return m.Update(cfg)
}

func (m *Manager) save(cfg Config) error {
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".task-notify-*.tmp")
	if err != nil { return err }
	name, ok := tmp.Name(), false
	defer func() { if !ok { _ = os.Remove(name) } }()
	if err := tmp.Chmod(0o600); err != nil { _ = tmp.Close(); return err }
	if _, err := tmp.Write(append(raw, '\n')); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Sync(); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	if err := os.Rename(name, m.path); err != nil { return err }
	ok = true
	return nil
}

func validateWebhook(raw string) error {
	if strings.TrimSpace(raw) == "" { return nil }
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" { return errors.New("必须是 http/https URL") }
	return nil
}

func (m *Manager) NotifyFailure(event schedtask.FailureEvent) {
	if event.RunID == "" { event.RunID = event.TaskID + ":" + event.StartedAt }
	m.mu.Lock()
	if _, ok := m.seen[event.RunID]; ok { m.mu.Unlock(); return }
	m.seen[event.RunID] = time.Now()
	for id, at := range m.seen { if time.Since(at) > 24*time.Hour { delete(m.seen, id) } }
	cfg := m.cfg
	m.mu.Unlock()
	if !cfg.Enabled { return }
	message := fmt.Sprintf("Kairo 定时任务失败\n任务：%s\n状态：%s\n时间：%s\n原因：%s", event.TaskName, event.Status, event.FinishedAt, event.Error)
	if cfg.WindowsToast { popup.Show(message) }
	if cfg.WeComWebhook != "" { go m.postWebhook(cfg.WeComWebhook, map[string]any{"msgtype": "text", "text": map[string]string{"content": message}}) }
	if cfg.DingWebhook != "" { go m.postWebhook(cfg.DingWebhook, map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": "Kairo 定时任务失败", "text": strings.ReplaceAll(message, "\n", "\n\n")}}) }
}

func (m *Manager) Test() error {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.WindowsToast && cfg.WeComWebhook == "" && cfg.DingWebhook == "" { return errors.New("尚未配置任何告警通道") }
	if cfg.WindowsToast { popup.Show("Kairo 告警通道测试\n这是一条测试通知") }
	var first error
	items := []struct{ url string; body any }{
		{cfg.WeComWebhook, map[string]any{"msgtype": "text", "text": map[string]string{"content": "Kairo 告警通道测试\n这是一条测试通知"}}},
		{cfg.DingWebhook, map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": "Kairo 告警通道测试", "text": "这是一条测试通知"}}},
	}
	for _, item := range items {
		if item.url == "" { continue }
		if err := m.postWebhookSync(item.url, item.body); err != nil && first == nil { first = err }
	}
	return first
}

func (m *Manager) postWebhook(raw string, body any) {
	for i := 0; i < 3; i++ {
		if err := m.postWebhookSync(raw, body); err == nil { return }
		time.Sleep(time.Duration(i+1) * 300 * time.Millisecond)
	}
}

func (m *Manager) postWebhookSync(raw string, body any) error {
	encoded, err := json.Marshal(body); if err != nil { return err }
	req, err := http.NewRequest(http.MethodPost, raw, bytes.NewReader(encoded)); if err != nil { return err }
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req); if err != nil { return err }
	defer resp.Body.Close(); _, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return fmt.Errorf("Webhook 返回 HTTP %d", resp.StatusCode) }
	return nil
}
