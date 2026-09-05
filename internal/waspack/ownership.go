package waspack

// Ownership metadata is kept outside the delivery directory. It lets the
// clean_kairo_artifacts policy distinguish a previous Kairo package from a
// user's list.txt/war/chmod.txt and therefore avoids name-based deletion.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ownershipEntry struct {
	Artifacts []string  `json:"artifacts"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ownershipDocument struct {
	Version int                       `json:"version"`
	Outputs map[string]ownershipEntry `json:"outputs"`
}

var ownershipMu sync.Mutex

func ownershipPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "kairo", "waspack-ownership.json")
	}
	return filepath.Join(os.TempDir(), "kairo-waspack-ownership.json")
}

func ownershipPathFor(metadataDir string) string {
	if strings.TrimSpace(metadataDir) != "" {
		return filepath.Join(metadataDir, "waspack-ownership.json")
	}
	return ownershipPath()
}

func loadOwnershipAt(path string) (ownershipDocument, error) {
	doc := ownershipDocument{Version: 1, Outputs: map[string]ownershipEntry{}}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	if len(raw) == 0 {
		return doc, fmt.Errorf("归属元数据为空或已损坏")
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version != 1 || doc.Outputs == nil {
		if err == nil {
			err = fmt.Errorf("版本或字段无效")
		}
		return doc, fmt.Errorf("归属元数据已损坏: %w", err)
	}
	return doc, nil
}

func loadOwnership() ownershipDocument {
	doc, _ := loadOwnershipAt(ownershipPath())
	return doc
}

func saveOwnership(doc ownershipDocument) error {
	return saveOwnershipAt(ownershipPath(), doc)
}

func saveOwnershipAt(path string, doc ownershipDocument) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".waspack-ownership-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceOwnershipFile(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func outputOwnershipKey(output string) string {
	key := ""
	if value, err := canonicalPath(output); err == nil {
		key = value
	} else {
		key, _ = filepath.Abs(output)
	}
	key = filepath.Clean(key)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

func replaceOwnershipFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// Windows cannot rename over an existing file. Move the old document out
	// of the way, then restore it if the second rename fails.
	old := dst + ".replace-backup"
	_ = os.Remove(old)
	hadOld := true
	if err := os.Rename(dst, old); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		hadOld = false
	}
	if err := os.Rename(src, dst); err != nil {
		if !hadOld {
			return err
		}
		if restoreErr := os.Rename(old, dst); restoreErr != nil {
			return fmt.Errorf("替换文件失败: %w；恢复旧文件失败: %v", err, restoreErr)
		}
		return err
	}
	if err := os.Remove(old); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("新文件已安装但旧文件备份无法清理: %w", err)
	}
	return nil
}

func recordOwnedArtifacts(output string, artifacts []string) error {
	return reconcileOwnedArtifactsAt(output, "", artifacts)
}

func recordOwnedArtifactsAt(output, metadataDir string, artifacts []string) error {
	return reconcileOwnedArtifactsAt(output, metadataDir, artifacts)
}

// reconcileOwnedArtifactsAt records only owned names which still exist as
// direct children of output. This is important after a package switch: a name
// removed by a successful replacement must not later authorize deletion of a
// user's newly-created file with the same name.
func reconcileOwnedArtifactsAt(output, metadataDir string, artifacts []string) error {
	ownershipMu.Lock()
	defer ownershipMu.Unlock()
	path := ownershipPathFor(metadataDir)
	doc, err := loadOwnershipAt(path)
	if err != nil {
		return err
	}
	key := outputOwnershipKey(output)
	entry := doc.Outputs[key]
	candidates := make(map[string]bool, len(entry.Artifacts)+len(artifacts))
	for _, name := range entry.Artifacts {
		if validOwnedChildName(name) {
			candidates[name] = true
		}
	}
	for _, name := range artifacts {
		if validOwnedChildName(name) {
			candidates[name] = true
		}
	}
	owned := make([]string, 0, len(candidates))
	for name := range candidates {
		if _, statErr := os.Lstat(filepath.Join(output, name)); statErr == nil {
			owned = append(owned, name)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	doc.Outputs[key] = ownershipEntry{Artifacts: owned, UpdatedAt: time.Now().UTC()}
	return saveOwnershipAt(path, doc)
}

func validOwnedChildName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	return !strings.ContainsAny(name, "/\\\x00\r\n")
}

func ownedArtifactsAt(output, metadataDir string) ([]string, bool, error) {
	ownershipMu.Lock()
	defer ownershipMu.Unlock()
	doc, err := loadOwnershipAt(ownershipPathFor(metadataDir))
	if err != nil {
		return nil, false, err
	}
	entry, ok := doc.Outputs[outputOwnershipKey(output)]
	if !ok {
		return nil, false, nil
	}
	for _, name := range entry.Artifacts {
		if !validOwnedChildName(name) {
			return nil, false, fmt.Errorf("归属元数据包含非法产物名")
		}
	}
	return append([]string(nil), entry.Artifacts...), true, nil
}

func ownsArtifacts(output string, artifacts []string) bool {
	return ownsArtifactsAt(output, "", artifacts)
}

func ownsArtifactsAt(output, metadataDir string, artifacts []string) bool {
	ownershipMu.Lock()
	defer ownershipMu.Unlock()
	doc, err := loadOwnershipAt(ownershipPathFor(metadataDir))
	if err != nil {
		return false
	}
	entry, ok := doc.Outputs[outputOwnershipKey(output)]
	if !ok {
		return false
	}
	set := make(map[string]bool, len(entry.Artifacts))
	for _, name := range entry.Artifacts {
		set[name] = true
	}
	for _, name := range artifacts {
		if !set[name] {
			return false
		}
	}
	return true
}

func ownershipError(output string) error {
	return fmt.Errorf("输出目录未被 Kairo 记录，拒绝清理未知归属文件: %s", output)
}
