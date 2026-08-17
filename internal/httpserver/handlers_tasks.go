package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"kairo/internal/schedtask"
)

// taskReq 是 API 入参 DTO，用 *bool 区分"未传 enabled"和"显式 enabled=false"
// （与 reminderReq 同一模式）。Add 缺省 enabled=true；Update 缺省保留原值。
type taskReq struct {
	Name       string `json:"name"`
	Enabled    *bool  `json:"enabled,omitempty"`
	Cron       string `json:"cron"`
	Command    string `json:"command"`
	WorkDir    string `json:"work_dir,omitempty"`
	TimeoutSec *int   `json:"timeout_sec,omitempty"`
}

func (r taskReq) toTask(enabledDefault bool) schedtask.Task {
	enabled := enabledDefault
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	out := schedtask.Task{
		Name:    strings.TrimSpace(r.Name),
		Enabled: enabled,
		Cron:    strings.TrimSpace(r.Cron),
		Command: r.Command, // 不在此 trim：多行脚本的缩进/换行有意义；Validate 里查空
		WorkDir: strings.TrimSpace(r.WorkDir),
	}
	if r.TimeoutSec != nil {
		out.TimeoutSec = *r.TimeoutSec
	}
	return out
}

// handleTaskDispatch 把定时任务路由统一收口。
//
// 路由：
//
//	GET    /api/tasks              列表（含 next_run_at / running）
//	POST   /api/tasks              新增（admin）
//	PUT    /api/tasks/{id}         更新（admin）
//	DELETE /api/tasks/{id}         删除（admin）
//	POST   /api/tasks/{id}/toggle  启用/停用切换（admin）
//	POST   /api/tasks/{id}/run     立即执行一次（admin，异步）
//	GET    /api/tasks/{id}/runs    运行历史（新的在前，最多 20 条）
func (s *Server) handleTaskDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tasks")

	switch {
	case path == "" || path == "/":
		switch r.Method {
		case http.MethodGet:
			s.handleTaskList(w, r)
		case http.MethodPost:
			if !requireAdmin(w, r) {
				return
			}
			s.handleTaskAdd(w, r)
		default:
			writeErr(w, 405, errors.New("仅支持 GET / POST"))
		}
	default:
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
				if !requireAdmin(w, r) {
					return
				}
				s.handleTaskUpdate(w, r, id)
			case http.MethodDelete:
				if !requireAdmin(w, r) {
					return
				}
				s.handleTaskDelete(w, r, id)
			default:
				writeErr(w, 405, errors.New("仅支持 PUT / DELETE"))
			}
		case "toggle":
			if r.Method != http.MethodPost {
				writeErr(w, 405, errors.New("仅支持 POST"))
				return
			}
			if !requireAdmin(w, r) {
				return
			}
			s.handleTaskToggle(w, r, id)
		case "run":
			if r.Method != http.MethodPost {
				writeErr(w, 405, errors.New("仅支持 POST"))
				return
			}
			if !requireAdmin(w, r) {
				return
			}
			s.handleTaskRun(w, r, id)
		case "runs":
			if r.Method != http.MethodGet {
				writeErr(w, 405, errors.New("仅支持 GET"))
				return
			}
			s.handleTaskRuns(w, r, id)
		default:
			writeErr(w, 404, fmt.Errorf("未知子路径：%s", action))
		}
	}
}

func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		writeJSON(w, 200, []schedtask.View{})
		return
	}
	writeJSON(w, 200, s.tasks.List())
}

func (s *Server) handleTaskAdd(w http.ResponseWriter, r *http.Request) {
	if s.tasks == nil {
		writeErr(w, 503, errors.New("定时任务服务未初始化"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req taskReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
		return
	}
	out, err := s.tasks.Add(req.toTask(true))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("task.add", "id", out.ID, "name", out.Name, "cron", out.Cron)
	writeJSON(w, 200, out)
}

// handleTaskUpdate PUT /api/tasks/:id — 全量替换定义字段。
//
// 契约说明（与 reminder 一致）：客户端 PUT 的 enabled 字段被静默忽略，
// 启停只能通过 POST /api/tasks/:id/toggle 走专用路径。
func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request, id string) {
	if s.tasks == nil {
		writeErr(w, 503, errors.New("定时任务服务未初始化"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req taskReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
		return
	}
	out, err := s.tasks.Update(id, req.toTask(false))
	if err != nil {
		if errors.Is(err, schedtask.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("task.update", "id", out.ID, "name", out.Name, "cron", out.Cron)
	writeJSON(w, 200, out)
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request, id string) {
	if s.tasks == nil {
		writeErr(w, 503, errors.New("定时任务服务未初始化"))
		return
	}
	if err := s.tasks.Delete(id); err != nil {
		if errors.Is(err, schedtask.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("task.delete", "id", id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleTaskToggle(w http.ResponseWriter, r *http.Request, id string) {
	if s.tasks == nil {
		writeErr(w, 503, errors.New("定时任务服务未初始化"))
		return
	}
	out, err := s.tasks.Toggle(id)
	if err != nil {
		if errors.Is(err, schedtask.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("task.toggle", "id", out.ID, "enabled", out.Enabled)
	writeJSON(w, 200, out)
}

func (s *Server) handleTaskRun(w http.ResponseWriter, r *http.Request, id string) {
	if s.tasks == nil {
		writeErr(w, 503, errors.New("定时任务服务未初始化"))
		return
	}
	if err := s.tasks.RunNow(id); err != nil {
		if errors.Is(err, schedtask.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		if errors.Is(err, schedtask.ErrRunning) {
			writeErr(w, 409, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	s.audit.Write("task.run.manual", "id", id)
	writeJSON(w, 200, map[string]any{"ok": true, "started": true})
}

func (s *Server) handleTaskRuns(w http.ResponseWriter, r *http.Request, id string) {
	if s.tasks == nil {
		writeErr(w, 503, errors.New("定时任务服务未初始化"))
		return
	}
	runs, err := s.tasks.Runs(id)
	if err != nil {
		if errors.Is(err, schedtask.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, runs)
}
