package sysutil

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// utf16UnitsForTest 返回 string 编码成 UTF-16 后的 unit 数（含 NUL 终止符），
// 用来验证 RegSzCbData 算出的字节数跟 buffer 实际大小一致。
//
// 用 unicode/utf16 而不是 syscall.UTF16FromString:前者跨平台,后者只在 windows
// 可用,我们这个测试要在 darwin CI 上也能跑。
func utf16UnitsForTest(s string) int {
	return len(utf16.Encode([]rune(s))) + 1
}

// TestRegSzCbData 覆盖 Bug 1a:旧实现用 (len(s)+1)*2 在非 ASCII 路径下
// 会算出偏大的 cbData,导致 RegSetValueExW 越界读内存。修复后 RegSzCbData
// 用 syscall.UTF16FromString 算真实 UTF-16 长度,各种字符路径下都精确。
//
// 关键 case：
//   - 纯 ASCII: 字节数 = UTF-16 unit 数(旧实现巧合正确)
//   - BMP 内中文（你好）: 3 字节/rune vs 1 unit/rune(旧实现偏大 4 字节/rune)
//   - BMP 外字符（😀,U+1F600）: 4 字节/rune vs 2 units/rune(旧实现偏大 4 字节/rune)
//   - 混合路径: ASCII + CJK + emoji
//   - 空格路径: 验证 Windows Run 键 space-split 问题
func TestRegSzCbData(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantCb   uint32 // 期望的 cbData 字节数
		wantUnit int    // 期望的 UTF-16 unit 数（含 NUL）
	}{
		{
			name:     "纯ASCII路径",
			input:    `C:\Program Files\Kairo\Kairo.exe`,
			// 数字符: C:\Program Files\Kairo\Kairo.exe
			// C(1)+:(1)+\(1)+Program(7)+' '(1)+Files(5)+\(1)+Kairo(5)+\(1)+Kairo(5)+.(1)+exe(3) = 32 字符
			// UTF-16 units = 32 + 1(NUL) = 33
			wantUnit: 33,
			wantCb:   66,
		},
		{
			name:     "BMP内中文路径",
			input:    `C:\我的工具\Kairo.exe`,
			// 数字符: C:\我的工具\Kairo.exe
			// C(1)+:(1)+\(1)+我的工具(4)+\(1)+Kairo(5)+.(1)+exe(3) = 17 字符
			// UTF-16 units = 17 + 1(NUL) = 18 (中文 BMP 内,各占 1 unit)
			wantUnit: 18,
			wantCb:   36,
		},
		{
			name:     "BMP外emoji路径",
			input:    `C:\😀\Kairo.exe`,
			// 数字符: C:\😀\Kairo.exe
			// C(1)+:(1)+\(1)+😀(1)+\(1)+Kairo(5)+.(1)+exe(3) = 14 字符
			// UTF-16 units: ASCII 1 unit/char,😀 占 2 units (surrogate pair)
			// = 13 + 2 + 1(NUL) = 16
			wantUnit: 16,
			wantCb:   32,
		},
		{
			name:     "混合路径(ASCII+中文+空格+emoji)",
			input:    `C:\我的 Tools\😀 app.exe`,
			// 数字符: C:\我的 Tools\😀 app.exe
			// C(1)+:(1)+\(1)+我的(2)+' '(1)+Tools(5)+\(1)+😀(1)+' '(1)+app(3)+.(1)+exe(3) = 21 字符
			// UTF-16 units: 18 ASCII/space + 2 (我的,各 1 unit) + 2 (😀,surrogate pair) + 1 NUL = 23
			wantUnit: 23,
			wantCb:   46,
		},
		{
			name:     "空字符串",
			input:    "",
			wantUnit: 1, // 只有 NUL
			wantCb:   2,
		},
		{
			name:     "纯中文(无分隔符)",
			input:    "你好",
			// 你 = 3字节/1unit, 好 = 3字节/1unit
			// units = 2+1(NUL) = 3
			wantUnit: 3,
			wantCb:   6,
		},
		{
			name:     "单emoji",
			input:    "😀",
			// 😀 = 4字节/2units
			// units = 2+1 = 3
			wantUnit: 3,
			wantCb:   6,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotCb, err := RegSzCbData(tc.input)
			if err != nil {
				t.Fatalf("RegSzCbData(%q) 返错: %v", tc.input, err)
			}
			if gotCb != tc.wantCb {
				t.Errorf("RegSzCbData(%q) = %d, want %d (UTF-16 units 含 NUL = %d, ASCII 字节数 = %d)",
					tc.input, gotCb, tc.wantCb, tc.wantUnit, len(tc.input))
			}
			// 关键不变量：cbData 永远等于 (UTF-16 units 数) * 2,
			// 不能比 ASCII 字节数算出来的小（否则会截断）。
			naiveCb := uint32((len(tc.input) + 1) * 2)
			if tc.name == "纯ASCII路径" || tc.name == "空字符串" {
				// 这两种 case 旧实现也恰好正确
				if gotCb != naiveCb {
					t.Logf("INFO: %q 旧实现也正确,新旧 cbData 都是 %d", tc.input, gotCb)
				}
			} else {
				// 非 ASCII case：新实现必须 >= 旧实现的 naiveCb（因为 UTF-16 unit 数 <= UTF-8 字节数对 BMP 内字符），
				// 实际上对中文/emoji，UTF-16 units 少于 UTF-8 字节，所以新实现 < 旧实现。
				// 关键断言：新实现 ≤ 旧实现，避免越界读。
				if gotCb > naiveCb {
					t.Errorf("RegSzCbData(%q) = %d 超过旧实现 naiveCb = %d, 仍会越界读", tc.input, gotCb, naiveCb)
				}
			}
		})
	}
}

// TestRegSzCbData_BufferAlignment 验证 RegSzCbData 算出的 cbData 跟
// UTF-16 buffer 实际大小完全一致 —— 这是修复 Bug 1a 的关键不变量
// （避免 RegSetValueExW 越界读）。
func TestRegSzCbData_BufferAlignment(t *testing.T) {
	inputs := []string{
		`C:\Program Files\Kairo\Kairo.exe`,
		`C:\我的工具\Kairo.exe`,
		`C:\😀\Kairo.exe`,
		`混合 ASCII + 中文 + emoji 😀 path with spaces.exe`,
		"",
		"你好世界",
		"😀😁😂🤣",
	}
	for _, s := range inputs {
		t.Run(s, func(t *testing.T) {
			cb, err := RegSzCbData(s)
			if err != nil {
				t.Fatalf("RegSzCbData(%q) 返错: %v", s, err)
			}
			// 重建 buffer 验证大小一致
			units := utf16UnitsForTest(s)
			wantCb := uint32(units * 2)
			if cb != wantCb {
				t.Errorf("RegSzCbData(%q) = %d, 但 UTF-16 buffer 实际 %d units = %d 字节 —— 越界风险!",
					s, cb, units, wantCb)
			}
		})
	}
}

// TestQuoteExePath 覆盖 Bug 1b:Run 键值必须包双引号防 path planting。
func TestQuoteExePath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "无空格路径",
			in:   `C:\Kairo\Kairo.exe`,
			want: `"C:\Kairo\Kairo.exe"`,
		},
		{
			name: "含空格路径(Program Files)",
			in:   `C:\Program Files\Kairo\Kairo.exe`,
			want: `"C:\Program Files\Kairo\Kairo.exe"`,
		},
		{
			name: "中文路径",
			in:   `C:\我的工具\Kairo.exe`,
			want: `"C:\我的工具\Kairo.exe"`,
		},
		{
			name: "emoji路径",
			in:   `C:\😀\Kairo.exe`,
			want: `"C:\😀\Kairo.exe"`,
		},
		{
			name: "相对路径",
			in:   `Kairo.exe`,
			want: `"Kairo.exe"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := QuoteExePath(tc.in)
			if got != tc.want {
				t.Errorf("QuoteExePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// 关键不变量:必须以 " 开头和结尾
			if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
				t.Errorf("QuoteExePath(%q) = %q, 缺引号包裹", tc.in, got)
			}
			// 关键不变量:除了首尾的引号,内容跟输入一致(os.Executable 返回的路径不会含 ")
			if got != `"`+tc.in+`"` {
				t.Errorf("QuoteExePath(%q) = %q, 内部内容被改", tc.in, got)
			}
		})
	}
}
