package sshclient

import (
	"path/filepath"
	"testing"
)

func TestRememberAndRemembered_InMemory(t *testing.T) {
	SetProfileStoreDir("") // 禁用持久化，只测内存路径
	rememberProfile("1.2.3.4:22", "legacy-dh-sha1-first")

	name, ok := rememberedProfile("1.2.3.4:22")
	if !ok || name != "legacy-dh-sha1-first" {
		t.Fatalf("expected legacy-dh-sha1-first, got %q ok=%v", name, ok)
	}

	if _, ok := rememberedProfile("9.9.9.9:22"); ok {
		t.Fatalf("unknown addr should not have memory")
	}
}

func TestRememberProfile_PersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	SetProfileStoreDir(dir)
	rememberProfile("10.0.0.1:2222", "no-ecdh")

	// 模拟"重启"：重置加载标记，让它重新从磁盘读。
	SetProfileStoreDir(dir)
	name, ok := rememberedProfile("10.0.0.1:2222")
	if !ok || name != "no-ecdh" {
		t.Fatalf("persist failed: got %q ok=%v", name, ok)
	}

	// 文件应落在 data 目录下
	got, err := filepath.Glob(filepath.Join(dir, profileStoreFileName))
	if err != nil || len(got) != 1 {
		t.Fatalf("expected profile store file to exist, err=%v glob=%v", err, got)
	}
}

func TestPrioritizeProfiles_MovesRememberedToFront(t *testing.T) {
	SetProfileStoreDir("")
	rememberProfile("h:22", "legacy-dh-sha1-first")

	all := sshCompatProfiles()
	if len(all) < 3 {
		t.Fatalf("expected >=3 profiles, got %d", len(all))
	}
	out := prioritizeProfiles("h:22", all)
	if out[0].Name != "legacy-dh-sha1-first" {
		t.Fatalf("expected legacy first, got %q", out[0].Name)
	}
	// 其余 profile 保持原顺序（不含已提前的那个）
	if len(out) != len(all) {
		t.Fatalf("length changed: %d -> %d", len(all), len(out))
	}
	rest := out[1:]
	for i, p := range rest {
		if p.Name == "legacy-dh-sha1-first" {
			t.Fatalf("remembered profile duplicated in rest at %d", i)
		}
	}
}

func TestPrioritizeProfiles_NoMemory(t *testing.T) {
	SetProfileStoreDir("")
	all := sshCompatProfiles()
	out := prioritizeProfiles("unknown:22", all)
	if out[0].Name != all[0].Name {
		t.Fatalf("no memory should keep original order, got %q want %q", out[0].Name, all[0].Name)
	}
}

func TestPrioritizeProfiles_SingleProfileNoReorder(t *testing.T) {
	SetProfileStoreDir("")
	rememberProfile("h:22", "no-ecdh")
	single := sshCompatProfiles()[:1]
	out := prioritizeProfiles("h:22", single)
	if len(out) != 1 || out[0].Name != single[0].Name {
		t.Fatalf("single profile should not reorder, got %v", out)
	}
}

func TestPrioritizeProfiles_StaleMemoryIgnored(t *testing.T) {
	SetProfileStoreDir("")
	rememberProfile("h:22", "nonexistent-profile")
	all := sshCompatProfiles()
	out := prioritizeProfiles("h:22", all)
	if out[0].Name != all[0].Name {
		t.Fatalf("stale memory should be ignored, got %q want %q", out[0].Name, all[0].Name)
	}
}
