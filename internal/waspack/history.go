package waspack

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultHistoryRetention = 200

type HistoryRequest struct {
	ProjectDir   string `json:"project_dir,omitempty"`
	OutputDir    string `json:"output_dir,omitempty"`
	PackageName  string `json:"package_name,omitempty"`
	Manifest     string `json:"manifest,omitempty"`
	AutoPair     bool   `json:"auto_pair"`
	PackType     string `json:"pack_type,omitempty"`
	BatchBaseDir string `json:"batch_base_dir,omitempty"`
	ChmodMode    string `json:"chmod_mode,omitempty"`
	IncludeZip   bool   `json:"include_zip,omitempty"`
	OutputPolicy string `json:"output_policy,omitempty"`
}

type ArtifactDigest struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type HistoryRecord struct {
	ID              string           `json:"id"`
	CreatedAt       time.Time        `json:"created_at"`
	CompletedAt     time.Time        `json:"completed_at,omitempty"`
	Operation       string           `json:"operation"`
	Status          string           `json:"status"`
	Request         HistoryRequest   `json:"request"`
	Files           int              `json:"files,omitempty"`
	Bytes           int64            `json:"bytes,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Error           string           `json:"error,omitempty"`
	Artifacts       []ArtifactDigest `json:"artifacts,omitempty"`
	SourceHistoryID string           `json:"source_history_id,omitempty"`
}

type historyDocument struct {
	Version int             `json:"version"`
	Records []HistoryRecord `json:"records"`
}

type HistoryStore struct {
	mu        sync.Mutex
	path      string
	retention int
}

// NewHistoryStore accepts an exact JSON file path, which keeps tests isolated
// and lets the HTTP server place it under its configured DataDir.
func NewHistoryStore(path string) *HistoryStore {
	return &HistoryStore{path: path, retention: DefaultHistoryRetention}
}

func (s *HistoryStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func NewHistoryID() string {
	var b [12]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(b[:])
}

func SnapshotRequest(req Request) HistoryRequest {
	return HistoryRequest{ProjectDir: req.ProjectDir, OutputDir: req.OutputDir, PackageName: req.PackageName, Manifest: req.Manifest, AutoPair: req.AutoPair, PackType: req.PackType, BatchBaseDir: req.BatchBaseDir, ChmodMode: req.ChmodMode, IncludeZip: req.IncludeZip, OutputPolicy: normalizeOutputPolicy(req.OutputPolicy)}
}

func NewHistoryRecord(operation string, req Request) HistoryRecord {
	return HistoryRecord{ID: NewHistoryID(), CreatedAt: time.Now().UTC(), Operation: operation, Status: "failure", Request: SnapshotRequest(req)}
}

func (s *HistoryStore) Append(record HistoryRecord) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return errors.New("打包历史存储未初始化")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	if record.ID == "" {
		record.ID = NewHistoryID()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.CompletedAt.IsZero() {
		record.CompletedAt = time.Now().UTC()
	}
	if record.Status != "success" && record.Status != "failure" {
		return errors.New("历史状态非法")
	}
	if !isHistoryOperation(strings.ToLower(strings.TrimSpace(record.Operation))) {
		return errors.New("历史操作不能为空")
	}
	record.Operation = strings.ToLower(strings.TrimSpace(record.Operation))
	// Never let callers accidentally persist replay authority.
	record.Request.OutputPolicy = normalizeOutputPolicy(record.Request.OutputPolicy)
	record.Request.Manifest = strings.ReplaceAll(strings.ReplaceAll(record.Request.Manifest, "\x00", ""), "\r\n", "\n")
	doc.Records = append([]HistoryRecord{cloneHistoryRecord(record)}, doc.Records...)
	retention := s.retention
	if retention <= 0 {
		retention = DefaultHistoryRetention
	}
	if len(doc.Records) > retention {
		doc.Records = doc.Records[:retention]
	}
	return s.writeLocked(doc)
}

func (s *HistoryStore) List(limit int, status, operation string) ([]HistoryRecord, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return nil, errors.New("打包历史存储未初始化")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	status, operation = strings.ToLower(strings.TrimSpace(status)), strings.ToLower(strings.TrimSpace(operation))
	if status != "" && status != "success" && status != "failure" {
		return nil, errors.New("status 只能是 success 或 failure")
	}
	if operation != "" && !isHistoryOperation(operation) {
		return nil, errors.New("operation 不受支持")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > DefaultHistoryRetention {
		limit = DefaultHistoryRetention
	}
	out := make([]HistoryRecord, 0, minHistory(limit, len(doc.Records)))
	for _, record := range doc.Records {
		if status != "" && strings.ToLower(record.Status) != status {
			continue
		}
		if operation != "" && strings.ToLower(record.Operation) != operation {
			continue
		}
		out = append(out, cloneHistoryRecord(record))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *HistoryStore) Get(id string) (HistoryRecord, error) {
	if err := validateHistoryID(id); err != nil {
		return HistoryRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return HistoryRecord{}, err
	}
	for _, record := range doc.Records {
		if record.ID == id {
			return cloneHistoryRecord(record), nil
		}
	}
	return HistoryRecord{}, os.ErrNotExist
}

func (s *HistoryStore) Delete(id string) error {
	if err := validateHistoryID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.loadLocked()
	if err != nil {
		return err
	}
	for i, record := range doc.Records {
		if record.ID == id {
			doc.Records = append(doc.Records[:i], doc.Records[i+1:]...)
			return s.writeLocked(doc)
		}
	}
	return os.ErrNotExist
}

func isHistoryOperation(op string) bool {
	switch op {
	case "build", "extract", "package", "zip", "rebuild":
		return true
	}
	return false
}
func validateHistoryID(id string) error {
	if id == "" || len(id) > 160 || strings.ContainsAny(id, "/\\\x00\r\n") {
		return errors.New("历史 ID 非法")
	}
	return nil
}
func minHistory(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (s *HistoryStore) loadLocked() (historyDocument, error) {
	doc := historyDocument{Version: 1, Records: []HistoryRecord{}}
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("读取打包历史失败: %w", err)
	}
	if len(raw) == 0 {
		return doc, errors.New("打包历史文件为空或已损坏")
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("解析打包历史失败（文件未覆盖）: %w", err)
	}
	if doc.Version != 1 || doc.Records == nil {
		return doc, errors.New("不支持的打包历史文件版本（文件未覆盖）")
	}
	sort.SliceStable(doc.Records, func(i, j int) bool { return doc.Records[i].CreatedAt.After(doc.Records[j].CreatedAt) })
	return doc, nil
}

func (s *HistoryStore) writeLocked(doc historyDocument) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化打包历史失败: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建打包历史目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".waspack-history-*.tmp")
	if err != nil {
		return fmt.Errorf("创建打包历史临时文件失败: %w", err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
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
	if err := replaceOwnershipFile(name, s.path); err != nil {
		return fmt.Errorf("原子替换打包历史失败: %w", err)
	}
	ok = true
	return nil
}

func cloneHistoryRecord(in HistoryRecord) HistoryRecord {
	out := in
	out.Warnings = append([]string(nil), in.Warnings...)
	out.Artifacts = append([]ArtifactDigest(nil), in.Artifacts...)
	return out
}

type artifactSpec struct{ Kind, Path string }

func DigestArtifact(kind, artifactPath string) (ArtifactDigest, error) {
	st, err := os.Stat(artifactPath)
	if err != nil {
		return ArtifactDigest{Kind: kind, Path: artifactPath}, err
	}
	if !st.Mode().IsRegular() {
		return ArtifactDigest{Kind: kind, Path: artifactPath}, fmt.Errorf("产物不是普通文件")
	}
	f, err := os.Open(artifactPath)
	if err != nil {
		return ArtifactDigest{Kind: kind, Path: artifactPath, Size: st.Size()}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ArtifactDigest{Kind: kind, Path: artifactPath, Size: st.Size()}, err
	}
	return ArtifactDigest{Kind: kind, Path: artifactPath, Size: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func digestSpecs(specs []artifactSpec) ([]ArtifactDigest, []string) {
	artifacts := make([]ArtifactDigest, 0, len(specs))
	warnings := []string{}
	for _, spec := range specs {
		digest, err := DigestArtifact(spec.Kind, spec.Path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("无法计算 %s SHA-256：%s", spec.Path, err))
			continue
		}
		artifacts = append(artifacts, digest)
	}
	return artifacts, warnings
}

func ResultArtifacts(res *Result) ([]ArtifactDigest, []string) {
	if res == nil {
		return nil, nil
	}
	specs := []artifactSpec{{"list", res.ListFile}, {"tar", res.TarFile}, {"backup_script", res.BackupScript}, {"execute_script", res.ExecuteScript}}
	if res.ChmodFile != "" {
		specs = append(specs, artifactSpec{"chmod", res.ChmodFile})
	}
	return digestSpecs(specs)
}

func ZipArtifacts(res *ZipResult) ([]ArtifactDigest, []string) {
	if res == nil {
		return nil, nil
	}
	return digestSpecs([]artifactSpec{{"zip", res.ZipFile}})
}
