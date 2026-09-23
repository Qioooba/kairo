package logquery

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// 本文件是 OPS-01 的验证入口：关键词字节必须能穿过 POSIX `printf %b` 原样还原，
// 且 dash 与 Bash 下得到相同的文件、行号和命中内容。
//
// 背景（实测，见 docs 的 OPS-01 表）：
//   - Bash 的 printf %b 支持非 POSIX 的 \xHH，\xd0 还原成单个字节 d0。
//   - dash 不支持 \xHH：有的构建原样输出字面量 "\xd0"；MSYS2/Git for Windows
//     构建把 \xd0 当字符码 U+00D0 再按当前 locale 编码，输出 c3 90 两个字节。
//     两种情况都会让 >= 0x80 的目标编码字节（GBK / UTF-8 中文）被改写。
//   - \0ooo（POSIX 规定的八进制形式）在两者下都还原成完全相同的原始字节。
//
// 因此 ToEncodingEscaped 只产出 \0ooo，本文件的测试同时覆盖"格式本身"和
// "真实 shell 执行结果一致"两层，后者显式指定 dash / bash 的绝对路径。

// ---------- 第一层：转义格式本身（不依赖任何 shell） ----------

// posixDecodePrintfB 只实现 POSIX printf %b 中本包会产出的转义：\0ddd。
// 用途是在没有 dash / bash 的机器上也能独立证明转义串无歧义、可逐字节还原。
func posixDecodePrintfB(t *testing.T, esc string) []byte {
	t.Helper()
	var out []byte
	for i := 0; i < len(esc); {
		if esc[i] != '\\' {
			t.Fatalf("第 %d 字节起不是转义序列（转义串必须全部是 ASCII 转义）: %q", i, esc)
		}
		if i+1 >= len(esc) || esc[i+1] != '0' {
			t.Fatalf("第 %d 字节起缺少 POSIX %%b 要求的 '\\0' 前缀: %q", i, esc)
		}
		// POSIX 最多读三位八进制数字。
		j := i + 2
		for j < len(esc) && j < i+5 && esc[j] >= '0' && esc[j] <= '7' {
			j++
		}
		digits := esc[i+2 : j]
		if len(digits) == 0 || len(digits) > 3 {
			t.Fatalf("八进制位数不合法 %q: %q", digits, esc)
		}
		v, err := strconv.ParseUint(digits, 8, 32)
		if err != nil || v > 0xff {
			t.Fatalf("八进制值越界 %q: %q", digits, esc)
		}
		// 非零字节必须固定三位，零字节用无歧义的 \000（两位）。
		if v == 0 {
			if len(digits) != 2 {
				t.Fatalf("零字节应写成 \\000，实际 %q: %q", digits, esc)
			}
		} else if len(digits) != 3 {
			t.Fatalf("非零字节 %d 必须是固定三位八进制，实际 %q: %q", v, digits, esc)
		}
		out = append(out, byte(v))
		i = j
	}
	return out
}

// 全部 256 个字节值必须无歧义地编码并还原——这是"每个原始字节固定三位八进制"
// 的穷尽验证，也是不能靠放弃编码换绿的最低保证。
func TestEscapeBytesOctal_AllByteValues(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	esc := escapeBytesOctal(all)

	if esc == "" {
		t.Fatal("256 字节的编码结果不应为空")
	}
	for _, r := range esc {
		if r > 0x7f {
			t.Fatalf("编码结果必须是纯 ASCII，出现 %q（U+%04X）", r, r)
		}
	}
	if strings.Contains(esc, `\x`) {
		t.Fatalf("不得再出现 Bash 扩展 \\xHH: %s", esc)
	}

	got := posixDecodePrintfB(t, esc)
	if len(got) != len(all) {
		t.Fatalf("还原字节数不符: got=%d want=%d", len(got), len(all))
	}
	for i := range all {
		if got[i] != all[i] {
			t.Fatalf("第 %d 个字节还原错误: got=0x%02x want=0x%02x", i, got[i], all[i])
		}
	}
}

func TestEscapeBytesOctal_Format(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"ASCII 大写 A", []byte("A"), `\0101`},
		{"NULL 字节用 \\000", []byte{0x00}, `\000`},
		{"NULL 后面跟 ASCII 数字不粘连", []byte{0x00, '0'}, `\000\0060`},
		{"最大字节 0xff", []byte{0xff}, `\0377`},
		{"GBK 信贷", []byte{0xd0, 0xc5}, `\0320\0305`},
		{"空输入", nil, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := escapeBytesOctal(c.in); got != c.want {
				t.Fatalf("escapeBytesOctal(% x) = %q，期望 %q", c.in, got, c.want)
			}
		})
	}
}

// ToEncodingEscaped 的对外契约：先按目标编码转字节，再产出纯 ASCII 的 POSIX 转义。
func TestToEncodingEscaped_POSIXOctalOutput(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		encoding string
		want     string
	}{
		{"utf-8 ASCII", "Exception", "utf-8", `\0105\0170\0143\0145\0160\0164\0151\0157\0156`},
		{"utf-8 中文", "信贷", "utf-8", `\0344\0277\0241\0350\0264\0267`},
		{"gbk 中文", "信贷系统", "gbk", `\0320\0305\0264\0373\0317\0265\0315\0263`},
		{"空串", "", "utf-8", ``},
		{"shell 元字符", `a<>b`, "utf-8", `\0141\0074\0076\0142`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ToEncodingEscaped(c.in, c.encoding)
			if err != nil {
				t.Fatalf("ToEncodingEscaped(%q, %q): %v", c.in, c.encoding, err)
			}
			if got != c.want {
				t.Fatalf("ToEncodingEscaped(%q, %q) = %q，期望 %q", c.in, c.encoding, got, c.want)
			}
			if strings.Contains(got, `\x`) {
				t.Fatalf("输出里不得出现 Bash 扩展 \\xHH: %q", got)
			}
			// 用独立的 POSIX 解析器还原，必须等于目标编码字节。
			enc, err := pickEncoder(c.encoding)
			if err != nil {
				t.Fatal(err)
			}
			var buf strings.Builder
			w := transform.NewWriter(&buf, enc.NewEncoder())
			if _, err := w.Write([]byte(c.in)); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			wantBytes := []byte(buf.String())
			if gotBytes := posixDecodePrintfB(t, got); string(gotBytes) != string(wantBytes) {
				t.Fatalf("还原字节 % x，期望 % x", gotBytes, wantBytes)
			}
		})
	}
}

func TestToEncodingEscaped_UnsupportedEncoding(t *testing.T) {
	if _, err := ToEncodingEscaped("x", "utf-16"); err == nil {
		t.Fatal("不支持的编码应报错")
	}
}

// 关键词必须仍然以编码形式出现，不能靠"把原文直接拼进 shell"换取通过。
func TestSearchCommand_KeywordStaysEncoded_OPS01(t *testing.T) {
	cases := []struct {
		query    string
		encoding string
		raw      string
	}{
		{"`touch>PWNED`", "utf-8", "`touch>PWNED`"},
		{"$(id)", "utf-8", "$(id)"},
		{"$USER", "utf-8", "$USER"},
		{"O'Brien", "utf-8", "O'Brien"},
		{"a<>b", "utf-8", "a<>b"},
		{"信贷系统", "utf-8", "信贷系统"},
		{"信贷系统", "gbk", "信贷系统"},
	}
	for _, c := range cases {
		t.Run(c.query+"/"+c.encoding, func(t *testing.T) {
			kw, err := ParseQuery(c.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", c.query, err)
			}
			cmd, err := SearchCommand("/dir", []string{"a.log"}, kw, 200, 30, c.encoding, false)
			if err != nil {
				t.Fatalf("SearchCommand: %v", err)
			}
			if strings.Contains(cmd, c.raw) {
				t.Fatalf("原文 %q 不得直接出现在命令里（必须保持编码）:\n%s", c.raw, cmd)
			}
			if !strings.Contains(cmd, `$(printf %b '\''\0`) {
				t.Fatalf("关键词必须以 POSIX 八进制转义经 printf %%b 传输:\n%s", cmd)
			}
		})
	}
}

// ---------- 第二层：显式指定 dash / bash 的真实执行 ----------

// targetShell 是差异验证里显式指定的 POSIX shell。
type targetShell struct {
	name string // 诊断名：dash / bash
	path string // 绝对路径
}

// findTargetShell 查找指定名字的 shell 可执行文件；找不到返回 ok=false。
//
// 调用方在 ok=false 时必须给出有原因的 skip，不能静默通过。
// 候选来源（跨平台，不写死某台机器的安装路径）：
//   - Linux/macOS 常见位置 /bin、/usr/bin、/usr/local/bin
//   - PATH（Windows 上 Git for Windows / MSYS2 / Cygwin 的 bash.exe 会在这里）
//   - PATH 上 sh 的同目录兄弟（Git for Windows 只保证 sh.exe 在 PATH 上，
//     但 usr/bin 下同时有 dash.exe / bash.exe，实测 dash.exe 是真正的 dash）
//   - Windows 上常见的 Git for Windows 安装根目录
//
// 说明：本仓库没有 WSL/Linux 专用测试入口（internal/diagnostics 只在文案里提到
// WSL），因此这里不通过 wsl.exe 转发；WSL 转发会把路径和编码再套一层，
// 验证结果不再对应远端 /bin/sh 的真实行为。
func findTargetShell(name string) (targetShell, bool) {
	var candidates []string
	add := func(p string) {
		if p != "" {
			candidates = append(candidates, p)
		}
	}
	add("/bin/" + name)
	add("/usr/bin/" + name)
	add("/usr/local/bin/" + name)
	for _, n := range []string{name, name + ".exe"} {
		if p, err := exec.LookPath(n); err == nil {
			add(p)
		}
	}
	for _, shName := range []string{"sh", "sh.exe"} {
		if p, err := exec.LookPath(shName); err == nil {
			dir := filepath.Dir(p)
			add(filepath.Join(dir, name))
			add(filepath.Join(dir, name+".exe"))
		}
	}
	for _, root := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LOCALAPPDATA"),
	} {
		if root == "" {
			continue
		}
		add(filepath.Join(root, "Git", "usr", "bin", name+".exe"))
	}
	for _, c := range candidates {
		st, err := os.Stat(c)
		if err != nil || st.IsDir() {
			continue
		}
		return targetShell{name: name, path: c}, true
	}
	return targetShell{}, false
}

// shellDecodeKeywordBytes 用指定 shell 的 printf %b 还原转义串，返回原始字节。
func shellDecodeKeywordBytes(t *testing.T, sh targetShell, esc string) []byte {
	t.Helper()
	// 不经过命令替换（$() 会吃掉 NUL 字节），直接读 stdout 原始字节。
	out, err := exec.Command(sh.path, "-c", "printf %b "+shellQuote(esc)).Output()
	if err != nil {
		t.Fatalf("shell %s (%s) 执行 printf %%b 失败: %v", sh.name, sh.path, err)
	}
	return out
}

// logShellEscapeCapability 把 shell 对 Bash 扩展 \xHH 的实际解释写进测试日志。
// 这既是 OPS-01 缺陷成因的现场证据，也是"远端 shell 能力"在测试侧的可诊断面。
func logShellEscapeCapability(t *testing.T, sh targetShell) {
	t.Helper()
	probe, err := exec.Command(sh.path, "-c", `printf %b '\x41\xd0'`).Output()
	if err != nil {
		t.Logf("shell %s (%s)：\\xHH 能力探测失败: %v", sh.name, sh.path, err)
		return
	}
	t.Logf("shell %s (%s)：printf %%b '\\x41\\xd0' 实际输出 % x（Bash 语义应为 41 d0）",
		sh.name, sh.path, probe)
}

// 关键词字节在真实 shell 下必须逐字节还原：ASCII、UTF-8 中文、GBK 中文、
// shell 元字符，以及穷尽的 0x00-0xff 全字节。
func TestRemoteShell_KeywordBytesDecode(t *testing.T) {
	for _, name := range []string{"dash", "bash"} {
		name := name
		t.Run(name, func(t *testing.T) {
			sh, ok := findTargetShell(name)
			if !ok {
				t.Skipf("本机未安装 %s（查找过 /bin、/usr/bin、/usr/local/bin、PATH、"+
					"PATH 上 sh 的同目录、%%ProgramFiles%%\\Git\\usr\\bin）；"+
					"OPS-01 的 shell 字节还原验证无法在该 shell 上执行", name)
			}
			logShellEscapeCapability(t, sh)

			cases := []struct {
				name     string
				in       string
				encoding string
			}{
				{"plain", "Exception", "utf-8"},
				{"chinese-utf8", "信贷系统 交易异常", "utf-8"},
				{"gbk", "信贷系统", "gbk"},
				{"metachars", "`touch>PWNED` $USER $(id) O'Brien a<>b", "utf-8"},
			}
			for _, c := range cases {
				esc, err := ToEncodingEscaped(c.in, c.encoding)
				if err != nil {
					t.Fatalf("ToEncodingEscaped(%q, %q): %v", c.in, c.encoding, err)
				}
				enc, err := pickEncoder(c.encoding)
				if err != nil {
					t.Fatal(err)
				}
				var buf strings.Builder
				w := transform.NewWriter(&buf, enc.NewEncoder())
				if _, err := w.Write([]byte(c.in)); err != nil {
					t.Fatal(err)
				}
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				want := []byte(buf.String())
				got := shellDecodeKeywordBytes(t, sh, esc)
				if string(got) != string(want) {
					t.Fatalf("%s/%s: %s 还原字节 % x，期望 % x（转义串 %s）",
						name, c.name, name, got, want, esc)
				}
			}

			// 穷尽 0x00-0xff：任何一个字节被改写都会被抓住。
			all := make([]byte, 256)
			for i := range all {
				all[i] = byte(i)
			}
			got := shellDecodeKeywordBytes(t, sh, escapeBytesOctal(all))
			if len(got) != 256 {
				t.Fatalf("%s: 全字节还原长度 %d，期望 256", name, len(got))
			}
			for i := range all {
				if got[i] != all[i] {
					t.Fatalf("%s: 第 %d 个字节还原为 0x%02x，期望 0x%02x", name, i, got[i], all[i])
				}
			}
		})
	}
}

// ---------- 第三层：dash / Bash 端到端搜索结果一致 ----------

// writeShellDifferentialFixture 生成差异验证共用的一批固定日志。
func writeShellDifferentialFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "app.log", []string{
		"INFO boot complete",             // 1
		"WARN disk almost full",          // 2
		"ERROR com.example+svc^ failed",  // 3
		"INFO retry scheduled",           // 4
		"DEBUG verbose dump",             // 5
		"ERROR timeout after 30s",        // 6
		"INFO `touch>PWNED` $USER $(id)", // 7
		"中文 信贷系统 交易异常",                   // 8
		"中文 交易失败 需要人工",                   // 9
		"ERROR 信贷系统 异常",                  // 10
	})
	writeFixture(t, dir, "win.log", []string{
		"START marker", // 1
		"窗口A alpha",    // 2
		"filler",       // 3
		"窗口B beta",     // 4
		"END marker",   // 5
	})
	writeFixture(t, dir, "repeat.log", []string{
		"dup term first",  // 1
		"noise",           // 2
		"dup term second", // 3
		"dup term third",  // 4
	})
	writeGBKFixture(t, dir, "gbk.log", "普通行\n信贷系统 交易异常\n信贷系统 系统维护\n")
	return dir
}

func writeGBKFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	w := transform.NewWriter(f, simplifiedchinese.GBK.NewEncoder())
	_, writeErr := w.Write([]byte(content))
	closeErr := w.Close()
	fileErr := f.Close()
	if writeErr != nil || closeErr != nil || fileErr != nil {
		t.Fatalf("写 GBK fixture: write=%v transformClose=%v fileClose=%v", writeErr, closeErr, fileErr)
	}
}

// shellScenario 是差异验证的一个搜索场景。
type shellScenario struct {
	name       string
	query      string
	files      []string
	encoding   string
	window     int // 0 表示同行搜索
	ignoreCase bool
	wantKeys   []string // 期望的 file:line 集合（顺序无关）
	wantLines  []string // 非空时再逐字节断言完整输出行（含命中内容）
	wantEmpty  bool
}

func ops01ShellScenarios() []shellScenario {
	return []shellScenario{
		{
			name: "普通词", query: "ERROR", files: []string{"app.log"}, encoding: "utf-8",
			wantKeys: []string{"app.log:10", "app.log:3", "app.log:6"},
			wantLines: []string{
				"app.log:10:ERROR 信贷系统 异常",
				"app.log:3:ERROR com.example+svc^ failed",
				"app.log:6:ERROR timeout after 30s",
			},
		},
		{
			name: "AND 布尔", query: "ERROR && timeout", files: []string{"app.log"}, encoding: "utf-8",
			wantKeys:  []string{"app.log:6"},
			wantLines: []string{"app.log:6:ERROR timeout after 30s"},
		},
		{
			name: "OR 布尔", query: "ERROR || DEBUG", files: []string{"app.log"}, encoding: "utf-8",
			wantKeys: []string{"app.log:10", "app.log:3", "app.log:5", "app.log:6"},
			wantLines: []string{
				"app.log:10:ERROR 信贷系统 异常",
				"app.log:3:ERROR com.example+svc^ failed",
				"app.log:5:DEBUG verbose dump",
				"app.log:6:ERROR timeout after 30s",
			},
		},
		{
			// 纯否定最容易出错：关键词还原不出来时行会被错误保留。
			name: "纯 NOT 布尔", query: "!DEBUG", files: []string{"app.log"}, encoding: "utf-8",
			wantKeys: []string{
				"app.log:1", "app.log:10", "app.log:2", "app.log:3", "app.log:4",
				"app.log:6", "app.log:7", "app.log:8", "app.log:9",
			},
		},
		{
			name: "AND + NOT", query: "INFO && !DEBUG", files: []string{"app.log"}, encoding: "utf-8",
			wantKeys: []string{"app.log:1", "app.log:4", "app.log:7"},
			wantLines: []string{
				"app.log:1:INFO boot complete",
				"app.log:4:INFO retry scheduled",
				"app.log:7:INFO `touch>PWNED` $USER $(id)",
			},
		},
		{
			name: "中文 UTF-8", query: "信贷系统", files: []string{"app.log"}, encoding: "utf-8",
			wantKeys: []string{"app.log:10", "app.log:8"},
			wantLines: []string{
				"app.log:10:ERROR 信贷系统 异常",
				"app.log:8:中文 信贷系统 交易异常",
			},
		},
		{
			name: "GBK 关键词", query: "信贷系统", files: []string{"gbk.log"}, encoding: "gbk",
			wantKeys: []string{"gbk.log:2", "gbk.log:3"},
		},
		{
			name: "跨行窗口", query: "窗口A && 窗口B", files: []string{"win.log"},
			encoding: "utf-8", window: 3,
			wantKeys: []string{"win.log:2", "win.log:4"},
		},
		{
			// 窗口成立时，窗口内的全部正关键词行都会返回：第 2 行只有"信贷系统"，
			// 第 3 行同时含"信贷系统"和"维护"，跨行跨度 1 ≤ window=2，两行都命中。
			name: "窗口 + GBK 关键词", query: "信贷系统 && 维护", files: []string{"gbk.log"},
			encoding: "gbk", window: 2,
			wantKeys: []string{"gbk.log:2", "gbk.log:3"},
		},
		{
			name: "重复词出现多次全部返回", query: "dup && term",
			files: []string{"repeat.log"}, encoding: "utf-8", window: 5,
			wantKeys:  []string{"repeat.log:1", "repeat.log:3", "repeat.log:4"},
			wantLines: nil,
		},
		{
			name: "重复词同行搜索", query: "dup", files: []string{"repeat.log"}, encoding: "utf-8",
			wantKeys: []string{"repeat.log:1", "repeat.log:3", "repeat.log:4"},
		},
		{
			name: "空结果", query: "NOSUCHTOKEN", files: []string{"app.log"},
			encoding: "utf-8", wantKeys: nil, wantEmpty: true,
		},
		{
			name: "元字符 反引号", query: "`touch>PWNED`", files: []string{"app.log"},
			encoding: "utf-8", wantKeys: []string{"app.log:7"},
			wantLines: []string{"app.log:7:INFO `touch>PWNED` $USER $(id)"},
		},
		{
			name: "元字符 命令替换", query: "$(id)", files: []string{"app.log"},
			encoding: "utf-8", wantKeys: []string{"app.log:7"},
		},
		{
			name: "元字符 变量", query: "$USER", files: []string{"app.log"},
			encoding: "utf-8", wantKeys: []string{"app.log:7"},
		},
		{
			name: "元字符 正则与单引号", query: "com.example+svc^", files: []string{"app.log"},
			encoding: "utf-8", wantKeys: []string{"app.log:3"},
		},
		{
			name: "忽略大小写", query: "error && TIMEOUT", files: []string{"app.log"},
			encoding: "utf-8", ignoreCase: true, wantKeys: []string{"app.log:6"},
		},
	}
}

// runScenarioUnderShell 生成真实搜索命令，取出内层脚本，交给指定 shell 执行。
func runScenarioUnderShell(t *testing.T, dir string, sh targetShell, sc shellScenario) string {
	t.Helper()
	kw, err := ParseQuery(sc.query)
	if err != nil {
		t.Fatalf("ParseQuery(%q): %v", sc.query, err)
	}
	var cmd string
	if sc.window > 0 {
		cmd, err = WindowSearchCommand(dir, sc.files, kw, 200, 30, sc.encoding, sc.ignoreCase, sc.window)
	} else {
		cmd, err = SearchCommand(dir, sc.files, kw, 200, 30, sc.encoding, sc.ignoreCase)
	}
	if err != nil {
		t.Fatalf("构造搜索命令失败: %v", err)
	}
	script := searchScriptFromCommand(t, cmd)
	out, err := exec.Command(sh.path, "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("shell %s 执行搜索失败: %v\nscript=%s\nout=%s", sh.name, err, script, out)
	}
	return string(out)
}

// searchScriptFromCommand 从 SearchCommand / WindowSearchCommand 生成的完整命令行里
// 取出真正交给远端 shell 执行的内层脚本。
//
// 生产命令形状固定为 `sh -c '<script>'`（buildSearchCommand 末尾拼装）：外层 shell
// 只负责把脚本交给远端 /bin/sh，而关键词字节的还原（KP_*=$(printf %b ...)）发生在这段
// 脚本里。差异验证要显式指定解释器，所以这里把外层 `sh -c` 拆掉，让 dash / bash 直接
// 解析同一段脚本文本；如果命令形状变了，这里会直接失败而不是悄悄跳过。
func searchScriptFromCommand(t *testing.T, cmd string) string {
	t.Helper()
	const prefix = "sh -c "
	if !strings.HasPrefix(cmd, prefix) {
		t.Fatalf("命令形状变了（期望前缀 %q）:\n%s", prefix, cmd)
	}
	q := cmd[len(prefix):]
	if len(q) < 2 || q[0] != '\'' || q[len(q)-1] != '\'' {
		t.Fatalf("命令不是单个 shellQuote 字符串:\n%s", q)
	}
	script := strings.ReplaceAll(q[1:len(q)-1], `'\''`, `'`)
	if !strings.Contains(script, "printf %b") {
		t.Fatalf("取出的脚本里没有 printf %%b，取脚本逻辑已失效:\n%s", script)
	}
	return script
}

func normalizedCommandOutput(out string) string {
	lines := strings.Split(out, "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if l == "" {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

func sortedHitKeysOPS01(t *testing.T, out string) []string {
	t.Helper()
	var keys []string
	for _, line := range strings.Split(normalizedCommandOutput(out), "\n") {
		if line == "" {
			continue
		}
		file, sep, ln, _, ok := ParseSearchHitLine(line)
		if !ok || sep != ':' {
			t.Fatalf("输出行无法解析: %q", line)
		}
		keys = append(keys, file+":"+strconv.Itoa(ln))
	}
	sort.Strings(keys)
	return keys
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 同一批固定日志在 dash 与 Bash 下必须返回相同的文件、行号与命中内容。
//
// 每个场景都在"本机找到的每个 shell"上真跑一遍并断言期望结果（这样单个 shell
// 的机器也有真实覆盖）；dash 与 Bash 都存在时再逐字节比较两边输出。
func TestSearchEngine_SameResultsAcrossShells(t *testing.T) {
	dir := writeShellDifferentialFixture(t)

	var shells []targetShell
	for _, name := range []string{"dash", "bash"} {
		if sh, ok := findTargetShell(name); ok {
			t.Logf("找到 shell %s: %s", name, sh.path)
			shells = append(shells, sh)
		} else {
			t.Logf("未找到 shell %s：相关场景会在该 shell 上跳过", name)
		}
	}

	for _, sc := range ops01ShellScenarios() {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			if len(shells) == 0 {
				t.Skip("本机没有找到 dash / bash（查找过 /bin、/usr/bin、/usr/local/bin、PATH、" +
					"PATH 上 sh 的同目录、%ProgramFiles%\\Git\\usr\\bin）；" +
					"命令执行层的 dash/Bash 一致性与正确性无法验证")
			}

			outputs := map[string]string{}
			for _, sh := range shells {
				out := runScenarioUnderShell(t, dir, sh, sc)
				outputs[sh.name] = normalizedCommandOutput(out)

				gotKeys := sortedHitKeysOPS01(t, out)
				if !sameStrings(gotKeys, sortedCopy(sc.wantKeys)) {
					t.Errorf("[%s] 文件:行号不符\n got=%v\nwant=%v\n完整输出:\n%s",
						sh.name, gotKeys, sortedCopy(sc.wantKeys), out)
				}
				if sc.wantEmpty && strings.TrimSpace(out) != "" {
					t.Errorf("[%s] 期望空结果，实际:\n%s", sh.name, out)
				}
				if sc.wantLines != nil {
					gotLines := strings.Split(normalizedCommandOutput(out), "\n")
					if len(gotLines) == 1 && gotLines[0] == "" {
						gotLines = nil
					}
					sort.Strings(gotLines)
					if !sameStrings(gotLines, sortedCopy(sc.wantLines)) {
						t.Errorf("[%s] 命中内容不符\n got=%q\nwant=%q", sh.name, gotLines, sortedCopy(sc.wantLines))
					}
				}
			}

			if len(shells) < 2 {
				t.Skipf("本机只找到 %d 个可用 shell，dash/Bash 一致性对比未执行"+
					"（单个 shell 的期望结果已在上方验证）", len(shells))
			}
			dashOut, okDash := outputs["dash"]
			bashOut, okBash := outputs["bash"]
			if !okDash || !okBash {
				t.Fatalf("dash/bash 输出缺失（dash=%v bash=%v），执行阶段已有失败", okDash, okBash)
			}
			if dashOut != bashOut {
				t.Errorf("dash 与 Bash 结果不一致（须逐字节相同）\n--- dash ---\n%s\n--- bash ---\n%s",
					dashOut, bashOut)
			}
		})
	}

	// 元字符场景不能真的执行了反引号 / 命令替换。
	t.Run("元字符未被执行", func(t *testing.T) {
		if _, err := os.Stat(filepath.Join(dir, "PWNED")); !os.IsNotExist(err) {
			t.Fatalf("反引号关键词被 shell 执行，PWNED 状态: %v", err)
		}
	})
}
