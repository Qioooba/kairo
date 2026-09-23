package comparefs

import (
	"testing"
	"time"
)

// 版本 token 必须绑定读取来源：size+mtime 相同也不代表同一个文件，
// 复制出来的文件、同一秒写入的不同文件都会得到同样的 size/mtime。
func TestEntryVersionCarriesPathIdentity(t *testing.T) {
	entry := Entry{Name: "B.txt", Path: "/left/B.txt", Size: 3, ModTime: time.Unix(0, 0).UTC()}
	version := entry.Version()
	if version.Path != "/left/B.txt" {
		t.Fatalf("version must bind the source path, got %q", version.Path)
	}
	// size+mtime 完全相同的两个文件，仅靠 SameVersion 无法区分。
	other := Entry{Name: "A.txt", Path: "/left/A.txt", Size: 3, ModTime: time.Unix(0, 0).UTC()}
	if !SameVersion(other, version) {
		t.Fatal("precondition: identical size/mtime must still pass SameVersion")
	}
	if SamePathIdentity(version.Path, other.Path) {
		t.Fatal("path identity must distinguish A from B")
	}
	if !SamePathIdentity(version.Path, "/left/./B.txt") {
		t.Fatal("the same path in another spelling must keep the same identity")
	}
	if !SamePathIdentity(`D:\Work\App\b.txt`, `d:/work/app/B.TXT`) {
		t.Fatal("windows-style paths must compare case-insensitively with unified separators")
	}
	if SamePathIdentity("", "/left/B.txt") || SamePathIdentity("/left/B.txt", "") {
		t.Fatal("an empty path has no identity and must not match a real path")
	}
}
