package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"ops-toolbox/internal/formatter"
)

type formatJSONReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`   // format | minify | validate
	Indent string `json:"indent"` // 缩进字符串，默认 "  "
}

func (s *Server) handleFormatJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatJSONReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}
	indent := req.Indent
	if indent == "" {
		indent = "  "
	}
	var out string
	var err error
	switch mode {
	case "format":
		out, err = formatter.FormatJSON(req.Input, indent)
	case "minify":
		out, err = formatter.MinifyJSON(req.Input)
	case "validate":
		if err = formatter.ValidateJSON(req.Input); err == nil {
			writeJSON(w, 200, map[string]any{"ok": true})
			return
		}
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify/validate"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/format/xml ----------

type formatXMLReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`
	Indent string `json:"indent"`
}

func (s *Server) handleFormatXML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatXMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}
	indent := req.Indent
	if indent == "" {
		indent = "  "
	}
	var out string
	var err error
	switch mode {
	case "format":
		out, err = formatter.FormatXML(req.Input, indent)
	case "minify":
		out, err = formatter.MinifyXML(req.Input)
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify"))
		return
	}
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/format/yaml ----------
//
// 支持模式：
//   - format：YAML 格式化（默认 indent=2）
//   - minify：单行紧凑
//   - validate：仅校验合法性
//   - to_json：YAML → JSON
//   - from_json：JSON → YAML
//
// 入参额外字段：
//   - indent：format / to_json / from_json 时控制缩进；
//     format / from_json 是 YAML 缩进空格数（默认 2，clamp 到 [1,8]）；
//     to_json 是 JSON 缩进字符串（默认 "  " 表示 2 空格）

type formatYAMLReq struct {
	Input  string `json:"input"`
	Mode   string `json:"mode"`   // format | minify | validate | to_json | from_json
	Indent string `json:"indent"` // 字符串，按 mode 解释
}

func (s *Server) handleFormatYAML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatYAMLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "format"
	}

	switch mode {
	case "format":
		indent, _ := strconv.Atoi(strings.TrimSpace(req.Indent))
		if indent <= 0 {
			indent = 2
		}
		out, err := formatter.FormatYAML(req.Input, indent)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "minify":
		out, err := formatter.MinifyYAML(req.Input)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "validate":
		if err := formatter.ValidateYAML(req.Input); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	case "to_json":
		indent := req.Indent
		if indent == "" {
			indent = "  "
		}
		out, err := formatter.YAMLToJSON(req.Input, indent)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "from_json":
		indent, _ := strconv.Atoi(strings.TrimSpace(req.Indent))
		if indent <= 0 {
			indent = 2
		}
		out, err := formatter.JSONToYAML(req.Input, indent)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	default:
		writeErr(w, 400, errors.New("mode 仅支持 format/minify/validate/to_json/from_json"))
	}
}

// ---------- /api/format/sql ----------
//
// 模式：format 固定（sql-formatter 不支持 minify；也不支持 validate——它对未识别
// token 走"原样保留"策略，所以合法性由数据库本身决定）。
//
// 入参额外字段（全部可选，sqlfmt.mjs 内部有默认值）：
//   - language：sql / mysql / postgresql / plsql / sqlite / tsql / bigquery /
//     snowflake / redshift / mariadb / db2 / spark / n1ql / trino / duckdb / transact
//     默认 sql
//   - keyword_case：upper / lower / preserve，默认 upper
//   - tab_width：缩进空格数，默认 2
//   - indent_style：standard / tabularLeft / tabularRight，默认 standard
//   - logical_operator_newline：before / after，默认 before
//   - lines_between_queries：多语句间隔，默认 2
//   - max_column_length：单行最大长度，默认 50

type formatSQLReq struct {
	Input string `json:"input"`
	formatter.SQLFormatOptions
	ScriptPath string `json:"script_path,omitempty"` // 高级：自定义 sqlfmt.mjs 路径
}

func (s *Server) handleFormatSQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatSQLReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	out, err := formatter.FormatSQL(req.Input, req.SQLFormatOptions, req.ScriptPath)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// ---------- /api/format/url-form ----------
//
// 模式：
//   - encode：map → "a=1&b=2"
//   - decode："a=1&b=2" → map（同名 key 自动合并成数组）
//
// 入参：
//   - encode 模式：input 是 JSON 对象字符串（前端解析后转 map；这里直接接受任意
//     key-value 输入比较麻烦，所以约定 input 是 JSON，handler 用 json.Unmarshal 解
//     到 map[string]string）
//   - decode 模式：input 是 raw query string

type formatURLFormReq struct {
	Input string `json:"input"`
	Mode  string `json:"mode"` // encode | decode
}

func (s *Server) handleFormatURLForm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, errors.New("仅支持 POST"))
		return
	}
	var req formatURLFormReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024)).Decode(&req); err != nil {
		writeErr(w, 400, err)
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "encode":
		// input 约定是 JSON 对象字符串，前端用 JSON.stringify(map) 即可
		raw := strings.TrimSpace(req.Input)
		if raw == "" {
			writeJSON(w, 200, map[string]any{"ok": true, "output": ""})
			return
		}
		var kv map[string]string
		if err := json.Unmarshal([]byte(raw), &kv); err != nil {
			writeErr(w, 400, fmt.Errorf("encode 模式 input 必须是 JSON 对象字符串：%w", err))
			return
		}
		out, err := formatter.URLFormEncode(kv)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "output": out})
	case "decode":
		out, err := formatter.URLFormDecode(req.Input)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		// 转回 JSON 给前端
		b, _ := json.MarshalIndent(out, "", "  ")
		writeJSON(w, 200, map[string]any{"ok": true, "output": string(b), "kv": out})
	default:
		writeErr(w, 400, errors.New("mode 仅支持 encode/decode"))
	}
}
