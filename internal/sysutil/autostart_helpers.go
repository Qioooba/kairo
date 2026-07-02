// Package sysutil / autostart_helpers.go — 跨平台可用的纯函数 helpers。
//
// 为什么单独放：regSzCbData 和 quoteExePath 不依赖任何注册表 API 或 syscall
// 平台调用，可以在 darwin / linux / windows 任意平台单测。
// autostart_windows.go 在 build 时把 regSzCbData 嵌入到 RegSetValueExW 的调用里，
// 测试在 darwin 上也能覆盖到 cbData 计算的正确性。
package sysutil

import (
	"unicode/utf16"
)

// RegSzCbData 计算把 string 写入 REG_SZ 时所需的字节数（含 NUL 终止符）。
//
// 关键：用 unicode/utf16.Encode 把 string 转成 UTF-16 unit 切片（含 NUL），
// cbData = (units+1) * 2 一定跟 buffer 大小一致。
//
// **不能**用 len(s)*2：s 是 Go string（UTF-8 字节数），跟 UTF-16 code unit
// 数在非 ASCII 路径下不等：
//   - BMP 内 CJK 字符（你好）：UTF-8 3 字节 / UTF-16 1 unit —— 用 len(s)*2 算
//     出来的 cbData 偏大（每个字符多 4 字节），RegSetValueExW 会越界读
//     buffer 之外的内存，把栈/堆上的残留数据写进注册表。
//   - BMP 外字符（emoji、罕用字）：UTF-8 4 字节 / UTF-16 2 units —— 同样的
//     越界读问题，每个字符多 4 字节。
//   - 纯 ASCII 路径：UTF-8 字节数 == UTF-16 unit 数，原来的写法刚好巧合不 bug。
//
// cbData 偏大本身不会写乱码到注册表（REG_SZ 遇 NUL 截断），但**会越界读
// 内存**，属于内存安全 bug。cbData 偏小才会写乱码（数据被截断到 cbData
// 字节数后写进去）—— 但旧实现偏大，所以也没乱码。
//
// 用 unicode/utf16 而非 syscall.UTF16FromString：前者跨平台（darwin/linux/
// windows 都能跑测试），后者只在 Windows 上可用。
func RegSzCbData(s string) (uint32, error) {
	units := utf16.Encode([]rune(s))
	return uint32((len(units) + 1) * 2), nil
}

// QuoteExePath 把 exe 路径包成 `"…"` 形式，给 Windows Run 键值用。
//
// 为什么必须加引号：Windows Run 键值是命令行字符串，会被按空白切分。
// `C:\Program Files\Kairo\kairo.exe` 不加引号 → 解析为 `C:\Program` +
// 参数 `Files\Kairo\kairo.exe`，再叠加 PATHEXT（.exe/.com/.bat/.cmd/.vbs ...），
// 如果 `C:\Program.exe` 存在就会被启动 —— 经典 path planting。
//
// 用双引号包整个路径后 Windows 识别为一个 token，按原意启动 Kairo。
// 读取时（IsAutoStartEnabled）只看大小不读内容，所以不需要对称的脱引号。
//
// 防御：os.Executable 返回的路径不会含 `"`，不做二次转义。
// 如果哪天要支持用户自定义路径且路径内含 `"`，需要改成：
//   - 用 `windows.CommandLineToArgvW` 解析后重新组装，或
//   - 把 `"` 转义成 `\"`（但 Run 键解析器不一定支持），或
//   - 拒绝写入并提示用户路径不能含引号。
func QuoteExePath(exePath string) string {
	return `"` + exePath + `"`
}

// UTF16UnitsForTest 仅供测试使用：返回 string 编码成 UTF-16 后的 unit 数
// （含 NUL 终止符），用来对比 RegSzCbData 的输出。
func UTF16UnitsForTest(s string) int {
	return len(utf16.Encode([]rune(s))) + 1
}
