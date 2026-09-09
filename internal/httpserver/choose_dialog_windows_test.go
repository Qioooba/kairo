//go:build windows

package httpserver

import (
	"os"
	"runtime"
	"testing"
	"unsafe"
)

func TestIFileOpenDialogVtblOffsets(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	h, _, _ := procOleInitialize.Call(0)
	if h != 0 && h != 1 {
		t.Fatalf("OleInitialize failed: 0x%x", h)
	}
	defer procOleUninitialize.Call()

	var dlg *iFileOpenDialog
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&dlg)),
	)
	if hr != 0 || dlg == nil {
		t.Fatalf("CoCreateInstance failed: 0x%x", hr)
	}
	defer dlg.release()

	// Cast the raw COM interface to an array of uintptr function pointers
	rawMethods := (*[30]uintptr)(unsafe.Pointer(dlg.vtbl))

	// Verify crucial vtable method offsets against Microsoft Windows SDK definition
	// IUnknown: 0: QueryInterface, 1: AddRef, 2: Release
	// IModalWindow: 3: Show
	if dlg.vtbl.Show != rawMethods[3] {
		t.Errorf("Show vtable offset mismatch: struct=0x%x, raw[3]=0x%x", dlg.vtbl.Show, rawMethods[3])
	}
	// IFileDialog:
	// 9: SetOptions
	if dlg.vtbl.SetOptions != rawMethods[9] {
		t.Errorf("SetOptions vtable offset mismatch: struct=0x%x, raw[9]=0x%x", dlg.vtbl.SetOptions, rawMethods[9])
	}
	// 11: SetDefaultFolder, 12: SetFolder
	if dlg.vtbl.SetDefaultFolder != rawMethods[11] {
		t.Errorf("SetDefaultFolder vtable offset mismatch: struct=0x%x, raw[11]=0x%x", dlg.vtbl.SetDefaultFolder, rawMethods[11])
	}
	if dlg.vtbl.SetFolder != rawMethods[12] {
		t.Errorf("SetFolder vtable offset mismatch: struct=0x%x, raw[12]=0x%x", dlg.vtbl.SetFolder, rawMethods[12])
	}
	// 17: SetTitle, 18: SetOkButtonLabel, 19: SetFileNameLabel, 20: GetResult
	if dlg.vtbl.SetTitle != rawMethods[17] {
		t.Errorf("SetTitle vtable offset mismatch: struct=0x%x, raw[17]=0x%x", dlg.vtbl.SetTitle, rawMethods[17])
	}
	if dlg.vtbl.SetOkButtonLabel != rawMethods[18] {
		t.Errorf("SetOkButtonLabel vtable offset mismatch: struct=0x%x, raw[18]=0x%x", dlg.vtbl.SetOkButtonLabel, rawMethods[18])
	}
	if dlg.vtbl.SetFileNameLabel != rawMethods[19] {
		t.Errorf("SetFileNameLabel vtable offset mismatch: struct=0x%x, raw[19]=0x%x", dlg.vtbl.SetFileNameLabel, rawMethods[19])
	}
	if dlg.vtbl.GetResult != rawMethods[20] {
		t.Errorf("GetResult vtable offset mismatch: struct=0x%x, raw[20]=0x%x (MUST be index 20, index 21 is AddPlace which causes crash on OK!)", dlg.vtbl.GetResult, rawMethods[20])
	}
	// 21: AddPlace
	if dlg.vtbl.AddPlace != rawMethods[21] {
		t.Errorf("AddPlace vtable offset mismatch: struct=0x%x, raw[21]=0x%x", dlg.vtbl.AddPlace, rawMethods[21])
	}
	// 27: GetResults, 28: GetSelectedItems
	if dlg.vtbl.GetResults != rawMethods[27] {
		t.Errorf("GetResults vtable offset mismatch: struct=0x%x, raw[27]=0x%x", dlg.vtbl.GetResults, rawMethods[27])
	}
	if dlg.vtbl.GetSelectedItems != rawMethods[28] {
		t.Errorf("GetSelectedItems vtable offset mismatch: struct=0x%x, raw[28]=0x%x", dlg.vtbl.GetSelectedItems, rawMethods[28])
	}
}

func TestIShellItemVtblOffsets(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	procOleInitialize.Call(0)
	defer procOleUninitialize.Call()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("skipping without user home dir")
	}
	item, err := shCreateItem(home)
	if err != nil {
		t.Fatalf("shCreateItem failed: %v", err)
	}
	defer item.release()

	rawMethods := (*[10]uintptr)(unsafe.Pointer(item.vtbl))

	// IShellItem: 0: QueryInterface, 1: AddRef, 2: Release, 3: BindToHandler, 4: GetParent, 5: GetDisplayName, 6: GetAttributes, 7: Compare
	if item.vtbl.GetDisplayName != rawMethods[5] {
		t.Errorf("GetDisplayName vtable offset mismatch: struct=0x%x, raw[5]=0x%x", item.vtbl.GetDisplayName, rawMethods[5])
	}

	path, err := item.filePath()
	if err != nil {
		t.Errorf("item.filePath failed: %v", err)
	}
	if path == "" {
		t.Errorf("item.filePath returned empty string")
	}
}
