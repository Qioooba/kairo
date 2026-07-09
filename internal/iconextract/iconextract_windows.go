//go:build windows

package iconextract

// Windows 实现：从 exe 提取主图标并转为 32x32 PNG。
//
// 流程：
//   1. SHGetFileInfo(exePath, 0, &shi, sizeof(shi), SHGFI_ICON | SHGFI_LARGEICON)
//      → 拿到 HICON（系统自动选最适合的尺寸，通常是 32x32）。
//   2. GetIconInfo(hIcon, &ii) → 拿到 color bitmap + mask bitmap。
//   3. GetDIBits(color) → 把 color bitmap 转成 BGRA 像素数组。
//   4. GetDIBits(mask) → alpha 通道（mask 里黑色=不透明，白色=透明）。
//   5. 合成 RGBA → 用 image/png 编码成 PNG bytes。
//
// 注意：
//   - 必须在 STA（CoInitialize）下调用 SHGetFileInfo；本包用 sync.Once 保证只初始化一次。
//   - GDI 对象（HBITMAP / HICON / HDC）必须显式 DeleteObject / DestroyIcon，否则泄漏。
//   - 若 SHGetFileInfo 失败（如文件不存在、无图标资源），返回错误，前端 fallback SVG。
//   - 不支持 .ico 多帧选择（系统会自动挑最合适的，通常是 32x32）。

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// SHGFI_ICON        = 0x00000100  // 取 HICON
	// SHGFI_LARGEICON   = 0x00000000  // 大图标（32x32，默认值）
	SHGFI_ICON      = 0x00000100
	SHGFI_LARGEICON = 0x00000000

	// BITMAPINFOHEADER: BI_RGB（无压缩）
	BI_RGB = 0

	// DIB_RGB_COLORS: 颜色表用 RGBQUAD（不用 palette index）
	DIB_RGB_COLORS = 0
)

// shfileinfo 对应 Win32 SHFILEINFOW（只用到 hIcon 字段，其他保留占位）。
type shfileinfo struct {
	HIcon       uintptr
	IIcon       int32
	DwAttributes uint32
	SzDisplayName [260]uint16
	SzTypeName     [80]uint16
}

// bitmapinfoheader 对应 Win32 BITMAPINFOHEADER。
type bitmapinfoheader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

// rgbquad 对应 Win32 RGBQUAD（颜色表项）。
type rgbquad struct {
	RgbBlue  byte
	RgbGreen byte
	RgbRed   byte
	RgbReserved byte
}

// iconinfo 对应 Win32 ICONINFO。
type iconinfo struct {
	FIcon    uint32 // BOOL
	XHotspot uint32
	YHotspot uint32
	HbmMask  uintptr // HBITMAP
	HbmColor uintptr // HBITMAP
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")

	procSHGetFileInfoW    = shell32.NewProc("SHGetFileInfoW")
	procDestroyIcon       = user32.NewProc("DestroyIcon")
	procGetIconInfo       = user32.NewProc("GetIconInfo")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC          = gdi32.NewProc("DeleteDC")
	procSelectObject      = gdi32.NewProc("SelectObject")
	procDeleteObject      = gdi32.NewProc("DeleteObject")
	procGetDIBits         = gdi32.NewProc("GetDIBits")
	procGetBitmapBits     = gdi32.NewProc("GetBitmapBits")
	procCoInitializeEx    = ole32.NewProc("CoInitializeEx")
	procCoUninitialize    = ole32.NewProc("CoUninitialize")

	COINIT_APARTMENTTHREADED = uintptr(0x2)
	S_OK               uintptr = 0x00000000
	S_FALSE            uintptr = 0x00000001
	RPC_E_CHANGED_MODE uint32  = 0x80010106

	coInitOnce sync.Once
	coInitOK   bool // 是否本进程初始化过 COM（用于决定退出时是否 CoUninitialize）
)

// ensureCOM 保证 SHGetFileInfo 在 STA 模式下调用。
// 实际上 SHGetFileInfo 文档没强制要求 COM 初始化，但实测某些场景（如 .lnk 解析）
// 会更稳定；为保险起见初始化一次，进程级常驻不 CoUninitialize。
func ensureCOM() {
	coInitOnce.Do(func() {
		hr, _, _ := procCoInitializeEx.Call(0, COINIT_APARTMENTTHREADED)
		if hr == S_OK || hr == S_FALSE {
			coInitOK = true
		} else if uint32(hr) == RPC_E_CHANGED_MODE {
			// 当前线程已经用别的模式初始化过 COM，继续用即可，不能 CoUninitialize。
			coInitOK = false
		}
	})
}

func extractPNG(exePath string) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ensureCOM()

	// 1) SHGetFileInfo 拿 HICON
	exePtr, err := windows.UTF16PtrFromString(exePath)
	if err != nil {
		return nil, fmt.Errorf("UTF16PtrFromString 失败: %w", err)
	}
	var shi shfileinfo
	flags := uintptr(SHGFI_ICON | SHGFI_LARGEICON)
	// SHGetFileInfoW(pszPath, dwFileAttributes, psfi, cbFileInfo, uFlags)
	ret, _, _ := procSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(exePtr)),
		0,
		uintptr(unsafe.Pointer(&shi)),
		unsafe.Sizeof(shi),
		flags,
	)
	// 返回值：成功返回非零（SHGFI_ICON 模式下返回值是"是否成功"）
	if ret == 0 || shi.HIcon == 0 {
		return nil, fmt.Errorf("SHGetFileInfo 未返回图标（文件不存在或无图标资源）")
	}
	defer procDestroyIcon.Call(shi.HIcon)

	// 2) GetIconInfo 拿 HBITMAP
	var ii iconinfo
	ret, _, _ = procGetIconInfo.Call(uintptr(shi.HIcon), uintptr(unsafe.Pointer(&ii)))
	if ret == 0 {
		return nil, fmt.Errorf("GetIconInfo 失败")
	}
	// GetIconInfo 返回的 HBITMAP 必须由调用方 DeleteObject
	if ii.HbmMask != 0 {
		defer procDeleteObject.Call(ii.HbmMask)
	}
	if ii.HbmColor != 0 {
		defer procDeleteObject.Call(ii.HbmColor)
	}

	// 3) 若没 color bitmap（纯 mask 单色图标），用 mask 当亮度
	hasColor := ii.HbmColor != 0
	primaryBmp := ii.HbmColor
	if !hasColor {
		primaryBmp = ii.HbmMask
	}
	if primaryBmp == 0 {
		return nil, fmt.Errorf("图标无可用 bitmap")
	}

	// 4) 通过 GetObjectW 拿 BITMAP 结构获取 width/height
	//    （GetDIBits 第一次调用传 0 行数时也会回填 bmi.biSizeImage + 不读像素，
	//     但拿 width/height 最稳妥的还是 GetObject）
	w, h, err := bitmapSize(primaryBmp)
	if err != nil {
		return nil, err
	}
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("bitmap 尺寸非法: %dx%d", w, h)
	}
	// 图标尺寸限制：超出 256x256 直接拒（防 GDI 内存爆掉）
	if w > 256 || h > 256 {
		return nil, fmt.Errorf("图标尺寸过大: %dx%d（>256）", w, h)
	}

	// 5) GetDIBits 读 color bitmap 像素（BGRA 32bpp）
	colorBytes, err := readBitmapBGRA(primaryBmp, w, h)
	if err != nil {
		return nil, err
	}

	// 6) 读 mask bitmap（如果有 color bitmap）当 alpha 通道
	var alphaBytes []byte
	if hasColor && ii.HbmMask != 0 {
		// mask 是单色 bitmap：每行按 word 对齐。h 可能是 color h 的 2 倍（XOR+AND 拼一起）
		maskH := h
		// 若 mask 高度是 color 的 2 倍，只取上半部分（XOR mask）
		mw, mh, _ := bitmapSize(ii.HbmMask)
		if mh == h*2 {
			maskH = h
		} else if mh == h {
			maskH = h
		}
		_ = mw
		alphaBytes, _ = readMaskAlpha(ii.HbmMask, w, maskH)
	}

	// 7) 组成 image.RGBA → PNG
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		// DIB 默认 bottom-up（biHeight > 0），第一行是图像底部
		srcRow := (h - 1 - y) * w * 4
		dstRow := y * w * 4
		for x := 0; x < w; x++ {
			b := colorBytes[srcRow+x*4+0]
			g := colorBytes[srcRow+x*4+1]
			r := colorBytes[srcRow+x*4+2]
			a := byte(255)
			if alphaBytes != nil {
				// mask：黑色像素(0)=不透明，白色像素(255)=透明
				maskVal := alphaBytes[(h-1-y)*w+x]
				if maskVal > 127 {
					a = 0 // 透明
				}
			}
			img.Pix[dstRow+x*4+0] = r
			img.Pix[dstRow+x*4+1] = g
			img.Pix[dstRow+x*4+2] = b
			img.Pix[dstRow+x*4+3] = a
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("png 编码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// bitmapSize 用 GetObjectW 拿 BITMAP {bmWidth, bmHeight}。
//
// Win32 BITMAP 结构（只取前两字段够用）：
//   typedef struct {
//     LONG bmWidth;
//     LONG bmHeight;
//     ...（其他字段省略）
//   } BITMAP;
type winBitmap struct {
	BmType       int32
	BmWidth      int32
	BmHeight     int32
	BmWidthBytes int32
	BmPlanes     uint16
	BmBitsPixel  uint16
	BmBits       uintptr
}

var procGetObjectW = gdi32.NewProc("GetObjectW")

func bitmapSize(hbmp uintptr) (int, int, error) {
	var bm winBitmap
	// GetObjectW(hgdiobj, cbBuffer, lpvObject)。cbBuffer=sizeof(BITMAP)=24
	ret, _, _ := procGetObjectW.Call(hbmp, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm)))
	if ret == 0 {
		return 0, 0, fmt.Errorf("GetObjectW 失败")
	}
	return int(bm.BmWidth), int(bm.BmHeight), nil
}

// readBitmapBGRA 用 GetDIBits 把 color bitmap 转成 32bpp BGRA bytes。
//
// 流程：CreateCompatibleDC → SelectObject(bitmap) → GetDIBits(2 次) → 还原 SelectObject → DeleteDC
func readBitmapBGRA(hbmp uintptr, w, h int) ([]byte, error) {
	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC 失败")
	}
	defer procDeleteDC.Call(hdc)

	oldObj, _, _ := procSelectObject.Call(hdc, hbmp)
	if oldObj == 0 {
		return nil, fmt.Errorf("SelectObject 失败")
	}
	defer procSelectObject.Call(hdc, oldObj)

	// BITMAPINFO = BITMAPINFOHEADER + RGBQUAD[1]（DIB_RGB_COLORS 模式下颜色表忽略，但结构体要保留 1 项空间）
	var bi struct {
		BmiHeader bitmapinfoheader
		BmiColors [1]rgbquad
	}
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = int32(w)
	bi.BmiHeader.BiHeight = int32(h) // 正数 = bottom-up
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 32
	bi.BmiHeader.BiCompression = BI_RGB

	pixelBytes := w * h * 4
	buf := make([]byte, pixelBytes)

	// 第一次调用传 lpvBits=nil：让系统回填 biSizeImage（非必需，但保险）
	procGetDIBits.Call(hdc, hbmp, 0, uintptr(h), 0, uintptr(unsafe.Pointer(&bi)), uintptr(DIB_RGB_COLORS))
	// 第二次调用真正读像素
	ret, _, _ := procGetDIBits.Call(hdc, hbmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), uintptr(DIB_RGB_COLORS))
	if ret == 0 {
		return nil, fmt.Errorf("GetDIBits 读像素失败")
	}
	return buf, nil
}

// readMaskAlpha 读单色 mask bitmap，转为 0/255 的 alpha 数组（每像素 1 字节）。
//
// 单色 bitmap 像素打包：每行字节数 = ((w + 15) / 16) * 2（按 word 对齐）。
// 每个字节表示 8 个像素，bit=0 黑色（不透明），bit=1 白色（透明）。
func readMaskAlpha(hbmp uintptr, w, h int) ([]byte, error) {
	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC(mask) 失败")
	}
	defer procDeleteDC.Call(hdc)

	oldObj, _, _ := procSelectObject.Call(hdc, hbmp)
	if oldObj == 0 {
		return nil, fmt.Errorf("SelectObject(mask) 失败")
	}
	defer procSelectObject.Call(hdc, oldObj)

	// 1bpp 单色 DIB
	var bi struct {
		BmiHeader bitmapinfoheader
		BmiColors [2]rgbquad // 2 项颜色表：[0]=黑(0,0,0,0)、[1]=白(255,255,255,0)
	}
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	bi.BmiHeader.BiWidth = int32(w)
	bi.BmiHeader.BiHeight = int32(h)
	bi.BmiHeader.BiPlanes = 1
	bi.BmiHeader.BiBitCount = 1
	bi.BmiHeader.BiCompression = BI_RGB
	bi.BmiColors[0] = rgbquad{0, 0, 0, 0}
	bi.BmiColors[1] = rgbquad{255, 255, 255, 0}

	// 行字节数按 word 对齐
	rowBytes := ((w + 15) / 16) * 2
	buf := make([]byte, rowBytes*h)

	procGetDIBits.Call(hdc, hbmp, 0, uintptr(h), 0, uintptr(unsafe.Pointer(&bi)), uintptr(DIB_RGB_COLORS))
	ret, _, _ := procGetDIBits.Call(hdc, hbmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), uintptr(DIB_RGB_COLORS))
	if ret == 0 {
		return nil, fmt.Errorf("GetDIBits(mask) 读像素失败")
	}

	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		srcRow := (h - 1 - y) * rowBytes
		dstRow := y * w
		for x := 0; x < w; x++ {
			byteIdx := srcRow + x/8
			bitIdx := 7 - (x % 8)
			bit := (buf[byteIdx] >> bitIdx) & 1
			// 1bpp + 颜色表 [0]=黑 / [1]=白
			// 我们要的是 alpha：bit=0（黑）→ 不透明(0)，bit=1（白）→ 透明(255)
			if bit == 1 {
				out[dstRow+x] = 255
			} else {
				out[dstRow+x] = 0
			}
		}
	}
	return out, nil
}
