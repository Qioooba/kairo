package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"kairo/internal/reminder"
)

// reminderReq 是 API 入参 DTO，用 *bool 区分"未传 enabled"和"显式 enabled=false"。
//
// JSON unmarshal 时，缺省字段是 nil（指针零值），显式传 false 是 &false。
// Add 行为：enabled 缺省视为 true（默认启用）。
// Update 行为：enabled 缺省保留当前值。
type reminderReq struct {
	Type       string `json:"type"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Content    string `json:"content"`
	At         string `json:"at,omitempty"`
	Weekdays   []int  `json:"weekdays,omitempty"`
	Time       string `json:"time,omitempty"`
	DayOfMonth *int   `json:"day_of_month,omitempty"`
}

// toReminder 把 DTO 转成 reminder.Reminder，enabled 缺省值由 caller 决定。
func (r reminderReq) toReminder(enabledDefault bool) reminder.Reminder {
	enabled := enabledDefault
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	out := reminder.Reminder{
		Type:       reminder.Type(strings.TrimSpace(r.Type)),
		Enabled:    enabled,
		Content:    strings.TrimSpace(r.Content),
		At:         strings.TrimSpace(r.At),
		Time:       strings.TrimSpace(r.Time),
		Weekdays:   r.Weekdays,
	}
	if r.DayOfMonth != nil {
		out.DayOfMonth = *r.DayOfMonth
	}
	return out
}

// reminderCtxPath 把 reminder 路由统一收口。
//
// 路由：
//   GET    /api/reminders              列表
//   POST   /api/reminders              新增（body 为 Reminder）
//   PUT    /api/reminders/{id}         更新
//   DELETE /api/reminders/{id}         删除
//   POST   /api/reminders/{id}/toggle  启用/禁用切换
//   POST   /api/reminders/{id}/fire    立即触发（测试用）
//   GET    /api/reminders/info         元信息（数据路径、调度状态）
func (s *Server) handleReminderDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/reminders")

	switch {
	case path == "" || path == "/":
		switch r.Method {
		case http.MethodGet:
			s.handleReminderList(w, r)
		case http.MethodPost:
			s.handleReminderAdd(w, r)
		default:
			writeErr(w, 405, errors.New("仅支持 GET / POST"))
		}
	case path == "/info":
		if r.Method != http.MethodGet {
			writeErr(w, 405, errors.New("仅支持 GET"))
			return
		}
		s.handleReminderInfo(w, r)
	case path == "/pause":
		switch r.Method {
		case http.MethodPost:
			s.handleReminderPause(w, r)
		case http.MethodDelete:
			s.handleReminderResume(w, r)
		default:
			writeErr(w, 405, errors.New("仅支持 POST / DELETE"))
		}
	default:
		// 形如 /{id} 或 /{id}/action
		parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)
		id := parts[0]
		if id == "" {
			writeErr(w, 404, errors.New("路径错误"))
			return
		}
		action := ""
		if len(parts) > 1 {
			action = parts[1]
		}
		switch action {
		case "":
			switch r.Method {
			case http.MethodPut:
				s.handleReminderUpdate(w, r, id)
			case http.MethodDelete:
				s.handleReminderDelete(w, r, id)
			default:
				writeErr(w, 405, errors.New("仅支持 PUT / DELETE"))
			}
		case "toggle":
			if r.Method != http.MethodPost {
				writeErr(w, 405, errors.New("仅支持 POST"))
				return
			}
			s.handleReminderToggle(w, r, id)
		case "fire":
			if r.Method != http.MethodPost {
				writeErr(w, 405, errors.New("仅支持 POST"))
				return
			}
			s.handleReminderFire(w, r, id)
		default:
			writeErr(w, 404, fmt.Errorf("未知子路径：%s", action))
		}
	}
}

func (s *Server) handleReminderList(w http.ResponseWriter, r *http.Request) {
	if s.reminders == nil {
		writeJSON(w, 200, []reminder.Reminder{})
		return
	}
	writeJSON(w, 200, s.reminders.List())
}

func (s *Server) handleReminderAdd(w http.ResponseWriter, r *http.Request) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req reminderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
		return
	}
	// Add 默认 enabled=true（用户主动添加的提醒默认开启）
	in := req.toReminder(true)
	out, err := s.reminders.Add(in)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("reminder.add",
		"id", out.ID,
		"type", string(out.Type),
		"content_len", len([]rune(out.Content)),
	)
	writeJSON(w, 200, out)
}

// handleReminderUpdate PUT /api/reminders/:id — 全量替换 content / schedule。
//
// 契约说明：客户端 PUT 的 enabled 字段会被静默忽略（即"缺省保留原值"）。
// 这是有意设计，由 reminder.Update() 在 manager 层统一执行，避免 PUT 与
// /toggle 双端点对同一字段的并发竞态。
//
// 启用 / 停用只能通过 POST /api/reminders/:id/toggle 走专用路径。
// 若调用方依赖「传 enabled:false 立即停用」会得到反直觉结果——这是契约而不是 bug。
func (s *Server) handleReminderUpdate(w http.ResponseWriter, r *http.Request, id string) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req reminderReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
		return
	}
	in := req.toReminder(false)
	out, err := s.reminders.Update(id, in)
	if err != nil {
		if errors.Is(err, reminder.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("reminder.update",
		"id", out.ID,
		"type", string(out.Type),
	)
	writeJSON(w, 200, out)
}

func (s *Server) handleReminderDelete(w http.ResponseWriter, r *http.Request, id string) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	if err := s.reminders.Delete(id); err != nil {
		if errors.Is(err, reminder.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("reminder.delete", "id", id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleReminderToggle(w http.ResponseWriter, r *http.Request, id string) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	out, err := s.reminders.Toggle(id)
	if err != nil {
		if errors.Is(err, reminder.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("reminder.toggle",
		"id", out.ID,
		"enabled", out.Enabled,
	)
	writeJSON(w, 200, out)
}

func (s *Server) handleReminderFire(w http.ResponseWriter, r *http.Request, id string) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	if err := s.reminders.FireNow(id); err != nil {
		if errors.Is(err, reminder.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("reminder.fire", "id", id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleReminderInfo(w http.ResponseWriter, r *http.Request) {
	storePath := ""
	paused := false
	pauseUntil := ""
	if s.reminders != nil {
		storePath = filepath.Join(s.cur().DataDir(), "reminders.json")
		paused, pauseUntil = s.reminders.PauseStatus()
	}
	writeJSON(w, 200, map[string]any{
		"store_path":  storePath,
		"queue_len":   -1, // 由 popup 包提供；目前无跨包接口，前端仅展示
		"server_time": time.Now().Format(time.RFC3339),
		"paused":      paused,
		"pause_until": pauseUntil,
	})
}

// handleReminderPause POST /api/reminders/pause
// body: {"until": "2026-07-08T00:00"}  // RFC3339，缺省为"明天 0 点"
func (s *Server) handleReminderPause(w http.ResponseWriter, r *http.Request) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req struct {
		Until string `json:"until"`
	}
	// 允许 body 为空（默认暂停到明天 0 点）
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
			return
		}
	}
	var until time.Time
	if req.Until != "" {
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			writeErr(w, 400, fmt.Errorf("until 格式错误（应为 RFC3339）：%w", err))
			return
		}
		until = t
	} else {
		// 默认：暂停到明天本地 00:00
		now := time.Now()
		y, m, d := now.Date()
		until = time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	}
	if until.Before(time.Now()) {
		writeErr(w, 400, errors.New("until 必须在未来"))
		return
	}
	s.reminders.PauseUntil(until)
	s.audit.Write("reminder.pause", "until", until.Format(time.RFC3339))
	writeJSON(w, 200, map[string]any{
		"ok":          true,
		"pause_until": until.Format(time.RFC3339),
	})
}

// handleReminderResume DELETE /api/reminders/pause
func (s *Server) handleReminderResume(w http.ResponseWriter, r *http.Request) {
	if s.reminders == nil {
		writeErr(w, 503, errors.New("reminder 服务未初始化"))
		return
	}
	s.reminders.PauseUntil(time.Time{})
	s.audit.Write("reminder.resume")
	writeJSON(w, 200, map[string]any{"ok": true})
}