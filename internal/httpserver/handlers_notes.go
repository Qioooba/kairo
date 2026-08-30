package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kairo/internal/note"
)

type noteCreateRequest struct {
	Title    string              `json:"title"`
	Body     string              `json:"body"`
	Color    string              `json:"color"`
	Pinned   bool                `json:"pinned"`
	Floating bool                `json:"floating"`
	Desktop  *note.DesktopLayout `json:"desktop,omitempty"`
}

type notePatchRequest struct {
	BaseRevision uint64          `json:"base_revision"`
	Title        *string         `json:"title,omitempty"`
	Body         *string         `json:"body,omitempty"`
	Color        *string         `json:"color,omitempty"`
	Pinned       *bool           `json:"pinned,omitempty"`
	Floating     *bool           `json:"floating,omitempty"`
	Archived     *bool           `json:"archived,omitempty"`
	Desktop      json.RawMessage `json:"desktop,omitempty"`
}

func (r notePatchRequest) patch() (note.Patch, error) {
	p := note.Patch{
		BaseRevision: r.BaseRevision,
		Title:        r.Title,
		Body:         r.Body,
		Color:        r.Color,
		Pinned:       r.Pinned,
		Floating:     r.Floating,
		Archived:     r.Archived,
	}
	if r.Desktop != nil {
		var layout *note.DesktopLayout
		if strings.TrimSpace(string(r.Desktop)) != "null" {
			layout = &note.DesktopLayout{}
			if err := json.Unmarshal(r.Desktop, layout); err != nil {
				return note.Patch{}, fmt.Errorf("desktop 格式错误: %w", err)
			}
		}
		p.Desktop = &layout
	}
	return p, nil
}

func (s *Server) handleNotesDispatch(w http.ResponseWriter, r *http.Request) {
	if s.notes == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("便笺服务未初始化"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/notes")
	switch {
	case path == "" || path == "/":
		switch r.Method {
		case http.MethodGet:
			s.handleNotesList(w, r)
		case http.MethodPost:
			s.handleNotesCreate(w, r)
		default:
			writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET / POST"))
		}
	case path == "/events":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 GET"))
			return
		}
		s.handleNotesEvents(w, r)
	default:
		parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/"), "/"), "/")
		if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
			writeErr(w, http.StatusNotFound, errors.New("便笺路径不存在"))
			return
		}
		id := parts[0]
		action := ""
		if len(parts) == 2 {
			action = parts[1]
		}
		if action == "" {
			s.handleNoteItem(w, r, id)
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, errors.New("仅支持 POST"))
			return
		}
		s.handleNoteAction(w, r, id, action)
	}
}

func (s *Server) handleNotesList(w http.ResponseWriter, r *http.Request) {
	f := note.Filter{Query: r.URL.Query().Get("q")}
	if v := r.URL.Query().Get("archived"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeErr(w, 400, errors.New("archived 必须是 true/false"))
			return
		}
		f.Archived = &b
	}
	if v := r.URL.Query().Get("floating"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeErr(w, 400, errors.New("floating 必须是 true/false"))
			return
		}
		f.Floating = &b
	}
	f.DesktopOnly = r.URL.Query().Get("desktop") == "true"
	writeJSON(w, 200, s.notes.List(f))
}

func (s *Server) handleNotesCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req noteCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
		return
	}
	out, err := s.notes.Add(note.Note{
		Title: req.Title, Body: req.Body, Color: req.Color,
		Pinned: req.Pinned, Floating: req.Floating, Desktop: req.Desktop,
	})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.audit.Write("note.add", "id", out.ID, "body_len", len([]rune(out.Body)))
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleNoteItem(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		out, ok := s.notes.Get(id)
		if !ok {
			writeErr(w, 404, note.ErrNotFound)
			return
		}
		writeJSON(w, 200, out)
	case http.MethodPatch:
		s.handleNotePatch(w, r, id, nil)
	case http.MethodDelete:
		if err := s.notes.Delete(id); err != nil {
			if errors.Is(err, note.ErrNotFound) {
				writeErr(w, 404, err)
			} else {
				writeErr(w, 500, err)
			}
			return
		}
		s.audit.Write("note.delete", "id", id)
		writeJSON(w, 200, map[string]any{"ok": true})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PATCH / DELETE"))
	}
}

func (s *Server) handleNotePatch(w http.ResponseWriter, r *http.Request, id string, mutate func(*notePatchRequest)) {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req notePatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体 JSON 解析失败: %w", err))
		return
	}
	if mutate != nil {
		mutate(&req)
	}
	p, err := req.patch()
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	out, err := s.notes.Patch(id, p)
	if err != nil {
		var conflict *note.ConflictError
		switch {
		case errors.As(err, &conflict):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "current": conflict.Current})
		case errors.Is(err, note.ErrNotFound):
			writeErr(w, 404, err)
		default:
			writeErr(w, 400, err)
		}
		return
	}
	s.audit.Write("note.update", "id", out.ID, "revision", out.Revision, "body_len", len([]rune(out.Body)))
	writeJSON(w, 200, out)
}

func (s *Server) handleNoteAction(w http.ResponseWriter, r *http.Request, id, action string) {
	switch action {
	case "archive":
		s.handleNotePatch(w, r, id, func(req *notePatchRequest) {
			if req.Archived == nil {
				v := true
				req.Archived = &v
			}
		})
	case "float":
		s.handleNotePatch(w, r, id, func(req *notePatchRequest) {
			if req.Floating == nil {
				v := true
				req.Floating = &v
			}
		})
	case "desktop":
		s.handleNotePatch(w, r, id, nil)
	default:
		writeErr(w, 404, fmt.Errorf("未知便笺动作: %s", action))
	}
}

func (s *Server) handleNotesEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, errors.New("SSE 不支持"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	events, unsubscribe := s.notes.Subscribe()
	defer unsubscribe()
	_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, b)
			flusher.Flush()
		case <-keepalive.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
