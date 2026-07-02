package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"kairo/internal/config"
	"kairo/internal/sysutil"
)

// ---------- /api/admin/autostart ----------
//
// v1.0 起新增：管理"开机自启动"开关。
//
// 设计动机：
//   - Kairo 是后台常驻工具，用户希望开机就启动、不用每次手动双击；
//   - 写注册表（HKCU\...\Run）由用户态可执行，避免 UAC 弹窗；
//   - 前端在系统配置页加一个"开机自启"勾选框，保存即生效；
//   - 主进程 main.go 启动尾部会再做一次 idempotent 同步：
//     如果用户上次勾选后注册表被外部清理（杀软 / 用户手动 regedit），
//     这次启动会自动恢复。
//
// 接口：
//   GET  /api/admin/autostart
//     → 200 {"enabled": <config 期望值>, "actual": <注册表实际值>,
//            "platform": "windows"/"darwin"/"linux",
//            "supported": <bool>, "key_name": <注册表路径>}
//
//   PUT  /api/admin/autostart
//     body: {"enabled": <bool>}
//     → 200 {"ok": true, "enabled": <新值>, "actual": <同步后实际值>,
//            "key_name": <注册表路径>}
//
//   - enabled = config 里的值（用户上次保存的偏好）；
//   - actual = 写完注册表后实际读到的状态（PUT 后一定 == enabled；
//     GET 时可能 != enabled —— 比如用户手动 regedit 清了）；
//   - supported = 该平台是否能写注册表（macOS/Linux 当前是 false，函数 no-op）；
//   - key_name 给 audit + 前端展示用。
//
// 鉴权：BE-003 风格 —— admin 角色才能改；普通 GET 允许所有人查（自启状态不算敏感）。
//
// 错误语义：
//   - enabled=true 且 SetAutoStart 失败（exePath 找不到 / 权限不够） → 500，
//     前端弹"启用失败：xxx"，让用户重试或重启 Kairo 重试。
//   - enabled=false 且 SetAutoStart 失败 → 同样 500，不让用户以为"取消成功"。
type adminAutoStartPutReq struct {
	Enabled bool `json:"enabled"`
}

type adminAutoStartView struct {
	Enabled   bool   `json:"enabled"`
	Actual    bool   `json:"actual"`
	Platform  string `json:"platform"`
	Supported bool   `json:"supported"`
	KeyName   string `json:"key_name,omitempty"`
	Error     string `json:"error,omitempty"` // IsAutoStartEnabled 读失败时填这个（GET 用）
}

func (s *Server) handleAdminAutoStart(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleAdminAutoStartGet(w, r)
	case http.MethodPut:
		// BE-003：写操作仅 admin 角色。
		if !requireAdmin(w, r) {
			return
		}
		s.handleAdminAutoStartPut(w, r)
	default:
		writeErr(w, 405, errors.New("仅支持 GET / PUT"))
	}
}

// handleAdminAutoStartGet GET 路径：不改任何状态，纯读取。
//
// 返回三件事：
//   1) config 里的期望值（enabled）—— "用户上次保存的偏好是什么"；
//   2) 注册表实际值（actual）—— "现在真正生效的是什么"；
//   3) 平台能力（supported）—— 非 Windows 永远 false，actual 也恒为 false。
//
// 这三者经常不一致：用户改了 config 但注册表失败；用户手动 regedit 删了；
// 用户从 Windows 拷贝到 macOS 跑（auto_start=true 但实际不可能）。
// 前端展示三态：✓ 已启用 / ⚠ 已禁用 / ✗ 平台不支持。
func (s *Server) handleAdminAutoStartGet(w http.ResponseWriter, r *http.Request) {
	cur := s.cur()
	view := adminAutoStartView{
		Enabled:   cur.App.AutoStart,
		Platform:  sysutil.PlatformName(),
		Supported: sysutil.Supported(),
		KeyName:   sysutil.AutoStartKeyName(),
	}
	if view.Supported {
		actual, err := sysutil.IsAutoStartEnabled()
		if err != nil {
			view.Error = err.Error()
		} else {
			view.Actual = actual
		}
	}
	writeJSON(w, 200, view)
}

// handleAdminAutoStartPut PUT 路径：更新 config.App.AutoStart + 同步注册表。
//
// 步骤：
//   1. 解析 body（仅 enabled 一个字段）；
//   2. 计算当前 exe 绝对路径（启用时需要，不启用不需要）；
//   3. 调 sysutil.SetAutoStart(enabled, exePath)；
//   4. 成功后通过 Manager.Replace 把 cfg.App.AutoStart 落盘；
//   5. 返回新状态（含 actual）让前端直接回显。
//
// 失败处理：
//   - 步骤 3 失败（写注册表）→ 不改 config（避免 config 标 true 但注册表实际未启用，
//     用户看不出问题）；直接 500，错误信息回前端。
//   - 步骤 4 失败（写 yaml）→ 这是"双源不一致"陷阱：注册表已写成功但 config 没更新。
//     旧实现直接返回 200 + warning 是个隐蔽 bug —— 用户的「禁用」会被下次启动的
//     main.go 同步逻辑静默还原（用户在「启用→禁用」方向上：注册表删值成功，
//     yaml 没记下 App.AutoStart=false → 下次启动按磁盘上 true 的旧值重新写回）。
//     修正：yaml 写盘失败时**回滚注册表到旧值**，让状态保持不变；前端看到 500 +
//     warning 提示「已回滚，请重试」。如果回滚也失败（极端情况：注册表被锁 / 杀软拦截），
//     审计日志会记 result=rollback_fail，由运维介入；前端返回的 500 文案明确告知
//     状态可能不一致，让用户重启 Kairo 后手动检查。
//
// 核心决策抽到 autoStartPutCore（接受 autostartOps 接口注入）方便单测；
// handler 只负责 HTTP 层（解码 req、写 resp、s.audit 调用）。
func (s *Server) handleAdminAutoStartPut(w http.ResponseWriter, r *http.Request) {
	var req adminAutoStartPutReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8*1024)).Decode(&req); err != nil {
		writeErr(w, 400, fmt.Errorf("请求体解析失败: %w", err))
		return
	}

	if !sysutil.Supported() {
		writeErr(w, 400, errors.New("当前平台不支持开机自启（仅 Windows）"))
		return
	}

	cur := s.cur()
	ops := &serverAutostartOps{server: s}
	result := autoStartPutCore(cur, req, ops, s.audit.Write)
	writeAutoStartResult(w, result)
}

// autostartOps 是 autoStartPutCore 依赖的 5 个外部操作。生产用 serverAutostartOps
// 调真实的 sysutil + os + cfg.Manager；测试用 mockAutostartOps 注入失败场景。
//
// 把 os.Executable() 抽到 ops 里也是为了让测试可控 —— 不用因为"测试时跑在
// /tmp 路径下"导致回滚逻辑走错分支。
type autostartOps interface {
	SetAutoStart(enabled bool, exePath string) error
	IsAutoStartEnabled() (bool, error)
	ReplaceConfig(newCfg *config.Config) error
	CurrentExe() (string, error)
	AutoStartKeyName() string
}

// serverAutostartOps 是 production 用的 autostartOps 实现，包装真实 sysutil + cfg + os。
type serverAutostartOps struct {
	server *Server
}

func (o *serverAutostartOps) SetAutoStart(enabled bool, exePath string) error {
	return sysutil.SetAutoStart(enabled, exePath)
}

func (o *serverAutostartOps) IsAutoStartEnabled() (bool, error) {
	return sysutil.IsAutoStartEnabled()
}

func (o *serverAutostartOps) ReplaceConfig(newCfg *config.Config) error {
	return o.server.cfg.Replace(newCfg)
}

func (o *serverAutostartOps) CurrentExe() (string, error) {
	raw, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(raw); err == nil {
		raw = resolved
	}
	return filepath.Clean(raw), nil
}

func (o *serverAutostartOps) AutoStartKeyName() string {
	return sysutil.AutoStartKeyName()
}

// autoStartPutResult 是 autoStartPutCore 的输出：HTTP 状态码 + JSON body。
type autoStartPutResult struct {
	Status int
	Body   map[string]any
}

// auditWriter 是 audit.Logger.Write 的类型别名（测试时可以用函数变量替换）。
type auditWriter func(op string, fields ...any)

// autoStartPutCore 实现 PUT /api/admin/autostart 的核心业务逻辑：
//   1. 读旧偏好
//   2. 计算 exePath（如果需要启用）
//   3. 写注册表
//   4. 回读 actual
//   5. 写 yaml；失败时回滚注册表到旧值
//
// 抽出来是为了测试：handler 负责 HTTP 适配，这个函数负责决策，测试可以注入
// mock 的 autostartOps 精确控制每一步成败。
func autoStartPutCore(cur *config.Config, req adminAutoStartPutReq, ops autostartOps, audit auditWriter) autoStartPutResult {
	oldEnabled := cur.App.AutoStart

	// 1. 计算 exePath
	var exePath string
	if req.Enabled {
		raw, err := ops.CurrentExe()
		if err != nil {
			return autoStartPutResult{
				Status: 500,
				Body:   map[string]any{"error": fmt.Sprintf("获取当前 exe 路径失败: %v", err)},
			}
		}
		exePath = raw
		if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(exePath), ".exe") {
			// Windows 上 os.Executable 应该总是返回 .exe 结尾。
			// 非 Windows 平台（开发环境）下 .exe 检查不适用，跳过。
			return autoStartPutResult{
				Status: 500,
				Body:   map[string]any{"error": fmt.Sprintf("异常 exe 路径（无 .exe 后缀）: %s", exePath)},
			}
		}
	}

	// 2. 写注册表
	if err := ops.SetAutoStart(req.Enabled, exePath); err != nil {
		audit("admin.autostart.put", "result", "fail",
			"enabled", req.Enabled,
			"err", err.Error(),
		)
		return autoStartPutResult{
			Status: 500,
			Body:   map[string]any{"error": fmt.Sprintf("写入注册表失败: %v", err)},
		}
	}

	// 3. 回读 actual（REVIEW-rc5 #8：err 不再忽略，记到 audit + 返给前端）
	newActual, actualErr := ops.IsAutoStartEnabled()
	actualErrStr := ""
	if actualErr != nil {
		actualErrStr = actualErr.Error()
	}

	// 4. 写 yaml
	newCfg := cur.Clone()
	newCfg.App.AutoStart = req.Enabled
	if err := ops.ReplaceConfig(newCfg); err != nil {
		// yaml 写盘失败 → 回滚注册表到 oldEnabled
		var rollbackExePath string
		if oldEnabled {
			if exePath != "" {
				rollbackExePath = exePath
			} else if raw, e := ops.CurrentExe(); e == nil {
				rollbackExePath = raw
			}
		}
		rollbackErr := ops.SetAutoStart(oldEnabled, rollbackExePath)
		if rollbackErr != nil {
			audit("admin.autostart.put", "result", "rollback_fail",
				"enabled", req.Enabled,
				"old_enabled", oldEnabled,
				"new_actual", newActual,
				"yaml_err", err.Error(),
				"rollback_err", rollbackErr.Error(),
			)
			return autoStartPutResult{
				Status: 500,
				Body: map[string]any{
					"error": fmt.Sprintf("配置文件保存失败: %v；注册表已尝试回滚到原状态但也失败 (%v)，状态可能不一致，请重启 Kairo 后手动检查注册表", err, rollbackErr),
				},
			}
		}
		audit("admin.autostart.put", "result", "rollback_ok",
			"enabled", req.Enabled,
			"old_enabled", oldEnabled,
			"new_actual", newActual,
			"yaml_err", err.Error(),
		)
		return autoStartPutResult{
			Status: 500,
			Body: map[string]any{
				"error": fmt.Sprintf("配置文件保存失败: %v；注册表已回滚到原状态（%s），请重试", err, oldEnabledStr(oldEnabled)),
			},
		}
	}

	audit("admin.autostart.put", "result", "ok",
		"enabled", req.Enabled,
		"actual", newActual,
		"actual_err", actualErrStr,
	)
	return autoStartPutResult{
		Status: 200,
		Body: map[string]any{
			"ok":        true,
			"enabled":   req.Enabled,
			"actual":    newActual,
			"actual_err": actualErrStr,
			"key_name":  ops.AutoStartKeyName(),
		},
	}
}

// writeAutoStartResult 把 autoStartPutResult 写到 HTTP 响应。
// 用 map[string]any 的"error"键还是"ok"键区分成功 / 失败，让前端能直接读 status code 判。
func writeAutoStartResult(w http.ResponseWriter, r autoStartPutResult) {
	writeJSON(w, r.Status, r.Body)
}

// oldEnabledStr 把 bool 翻译成中文"启用/禁用"，用于错误信息。
func oldEnabledStr(b bool) string {
	if b {
		return "启用"
	}
	return "禁用"
}