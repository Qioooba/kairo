package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"ops-toolbox/internal/config"
)

// ---------- /api/admin/openers ----------
//
// v0.8 起新增：管理"外部打开器"列表。
//
// 设计动机：
//   - 现有 /api/admin/servers 不接受 app 段（运维控制 vs 页面编辑边界）；
//   - 但用户希望从页面配置 external_openers（Notepad++ / IDEA / VS Code ...）；
//   - 单独开个 PUT 接口：req 只接受 openers 数组，handler 用 cur.Clone + 替换 App.ExternalOpeners
//   - Manager.Replace 原子写盘。
//   - 校验走 Manager.Replace 内部的 Validate（name 非空 / path 非空 / name 唯一），
//     非法值 400，不会写盘。
type adminOpenersPutReq struct {
	Openers []config.ExternalOpener `json:"openers"`
}

func (s *Server) handleAdminOpeners(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// GET 跟 /api/config 返回的 App.ExternalOpeners 重复但有用：
		// 下载历史页可能只想拉 openers（不要整个 config），
		// 给个独立端点避免下载历史页依赖整个 /api/config 的字段稳定性。
		// 【v0.x 修复 #B5】统一返回 [] 而非 null：Go 的 nil slice JSON 序列化为 null，
		// 前端期望数组，空配置场景下语义更清晰（前端 Array.isArray 兜底可去掉）。
		cur := s.cur()
		openers := cur.App.ExternalOpeners
		if openers == nil {
			openers = []config.ExternalOpener{}
		}
		writeJSON(w, 200, map[string]any{
			"openers": openers,
		})
	case http.MethodPut:
		var req adminOpenersPutReq
		if err := json.NewDecoder(io.LimitReader(r.Body, 256*1024)).Decode(&req); err != nil {
			writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
			return
		}
		// 允许 req.Openers 为空数组 = 清空所有打开器（用户主动清空场景）。
		// 区分 nil 和 [] 的语义：nil 视为"没传 openers 字段"，拒掉防误操作。
		if req.Openers == nil {
			writeErr(w, 400, errors.New("openers 字段必须存在（即使是空数组 []）"))
			return
		}
		cur := s.cur()
		newCfg := cur.Clone()
		newCfg.App.ExternalOpeners = req.Openers
		if err := s.cfg.Replace(newCfg); err != nil {
			s.audit.Write("admin.openers.put", "result", "fail", "err", err.Error())
			writeErr(w, 400, err)
			return
		}
		s.audit.Write("admin.openers.put", "result", "ok", "count", len(req.Openers))
		writeJSON(w, 200, map[string]any{
			"ok":    true,
			"count": len(req.Openers),
			"path":  s.cfg.Path(),
		})
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT"))
	}
}
