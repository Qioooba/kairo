package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"ops-toolbox/internal/config"
)

// ---------- /api/admin/servers ----------
//
// GET  — 返回完整 systems 树（与 /api/config 同样的内容）。
// PUT  — 用请求体整树替换内存配置，原子写回 config.yaml。
//
// 设计要点：
//   - PUT 不接受 app/search 段（这两个是运维控制，不开放给页面改）；
//     页面只需要管 systems 树。
//   - 写盘用 Manager.Replace 内部跑 Defaults + Validate，
//     校验失败直接 400，磁盘不会被破坏。
type adminServersPutReq struct {
	Systems []config.SystemConfig `json:"systems"`
}

func (s *Server) handleAdminServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cur := s.cur()
		writeJSON(w, 200, configView{
			App:     cur.App,
			Systems: cur.Systems,
			Search:  cur.Search,
		})
	case http.MethodPut:
		var req adminServersPutReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
			return
		}
		if len(req.Systems) == 0 {
			writeErr(w, 400, errors.New("至少需要一个系统"))
			return
		}
		// 用现有 App + Search + 新的 Systems 构造新 Config
		//
		// 必须 Clone：旧实现是 `newCfg := *cur; newCfg.Systems = req.Systems`，
		// 浅拷贝会让 newCfg.App.FreeFileRoots / newCfg.Systems[*].Servers[*].LogDirs[*].Patterns
		// 等 inner slice 跟老 cfg 共享底层 array。Put 后如果某处直接改
		// newCfg.App.FreeFileRoots[0] = "/etc"，老 reader 拿到的快照会被污染。
		// Clone() 深拷贝所有 inner slice + 指针字段，杜绝这种 aliasing。
		cur := s.cur()
		newCfg := cur.Clone()
		newCfg.Systems = req.Systems
		if err := s.cfg.Replace(newCfg); err != nil {
			s.audit.Write("admin.servers.put", "result", "fail", "err", err.Error())
			writeErr(w, 400, err)
			return
		}
		s.audit.Write("admin.servers.put", "result", "ok", "systems", len(req.Systems))
		writeJSON(w, 200, map[string]any{
			"ok":      true,
			"systems": len(req.Systems),
			"path":    s.cfg.Path(),
		})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT"))
	}
}
