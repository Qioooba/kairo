package waspack

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StageProvenance is deliberately kept outside the output directory. The WAR
// itself is user-editable, but its origin must remain bound to the exact
// extraction configuration used to create it.
type StageProvenance struct {
	Token          string    `json:"token"`
	OutputDir      string    `json:"output_dir"`
	ProjectDir     string    `json:"project_dir"`
	PackageName    string    `json:"package_name,omitempty"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	AutoPair       bool      `json:"auto_pair"`
	PackType       string    `json:"pack_type,omitempty"`
	BatchBaseDir   string    `json:"batch_base_dir,omitempty"`
	ChmodMode      string    `json:"chmod_mode,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type provenanceDocument struct {
	Version int                        `json:"version"`
	Outputs map[string]StageProvenance `json:"outputs"`
}

var provenanceMu sync.Mutex

func provenancePath(metadataDir string) string {
	return filepath.Join(metadataDir, "waspack-provenance.json")
}

type provenanceFingerprint struct {
	OutputDir      string `json:"output_dir"`
	ProjectDir     string `json:"project_dir"`
	PackageName    string `json:"package_name"`
	ManifestSHA256 string `json:"manifest_sha256"`
	AutoPair       bool   `json:"auto_pair"`
	PackType       string `json:"pack_type"`
	BatchBaseDir   string `json:"batch_base_dir"`
	ChmodMode      string `json:"chmod_mode"`
}

func makeStageFingerprint(req Request, outputDir string) (provenanceFingerprint, error) {
	if strings.TrimSpace(req.MetadataDir) == "" {
		return provenanceFingerprint{}, errors.New("抽取阶段元数据目录未配置")
	}
	project := strings.TrimSpace(req.ProjectDir)
	if project == "" {
		return provenanceFingerprint{}, errors.New("抽取阶段缺少工程目录")
	}
	projectAbs, err := filepath.Abs(project)
	if err != nil {
		return provenanceFingerprint{}, fmt.Errorf("工程目录无效: %w", err)
	}
	if st, statErr := os.Stat(projectAbs); statErr != nil || !st.IsDir() {
		if statErr != nil {
			return provenanceFingerprint{}, fmt.Errorf("工程目录无效: %w", statErr)
		}
		return provenanceFingerprint{}, errors.New("工程目录无效")
	}
	projectCanon, err := canonicalPath(projectAbs)
	if err != nil {
		return provenanceFingerprint{}, fmt.Errorf("工程目录无法解析: %w", err)
	}
	outputCanon, err := canonicalPath(outputDir)
	if err != nil {
		return provenanceFingerprint{}, fmt.Errorf("输出目录无法解析: %w", err)
	}
	manifestSum := sha256.Sum256([]byte(req.Manifest))
	packageName, err := SanitizePackageName(req.PackageName)
	if err != nil {
		return provenanceFingerprint{}, err
	}
	packType := strings.ToLower(strings.TrimSpace(req.PackType))
	if packType == "" {
		packType = "app"
	}
	return provenanceFingerprint{
		OutputDir:      outputCanon,
		ProjectDir:     projectCanon,
		PackageName:    packageName,
		ManifestSHA256: hex.EncodeToString(manifestSum[:]),
		AutoPair:       req.AutoPair,
		PackType:       packType,
		BatchBaseDir:   strings.TrimSpace(req.BatchBaseDir),
		ChmodMode:      strings.TrimSpace(req.ChmodMode),
	}, nil
}

// WriteStageProvenance records the just-published WAR and returns its opaque
// token. A corrupt existing store is an error and is never replaced.
func WriteStageProvenance(req Request, outputDir string) (string, error) {
	fingerprint, err := makeStageFingerprint(req, outputDir)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("create stage token: %w", err)
	}
	now := time.Now().UTC()
	seed, _ := json.Marshal(struct {
		Fingerprint provenanceFingerprint `json:"fingerprint"`
		CreatedAt   time.Time             `json:"created_at"`
		Nonce       string                `json:"nonce"`
	}{fingerprint, now, hex.EncodeToString(nonce)})
	sum := sha256.Sum256(seed)
	record := StageProvenance{
		Token:          "stage-" + hex.EncodeToString(sum[:]),
		OutputDir:      fingerprint.OutputDir,
		ProjectDir:     fingerprint.ProjectDir,
		PackageName:    fingerprint.PackageName,
		ManifestSHA256: fingerprint.ManifestSHA256,
		AutoPair:       fingerprint.AutoPair,
		PackType:       fingerprint.PackType,
		BatchBaseDir:   fingerprint.BatchBaseDir,
		ChmodMode:      fingerprint.ChmodMode,
		CreatedAt:      now,
	}

	provenanceMu.Lock()
	defer provenanceMu.Unlock()
	path := provenancePath(req.MetadataDir)
	doc, err := loadProvenance(path)
	if err != nil {
		return "", err
	}
	if doc.Outputs == nil {
		doc.Outputs = make(map[string]StageProvenance)
	}
	doc.Outputs[record.OutputDir] = record
	if err := writeProvenance(path, doc); err != nil {
		return "", err
	}
	return record.Token, nil
}

// ValidateStageProvenance checks both the stage token (when supplied) and the
// full extraction configuration. It intentionally does not hash WAR contents.
func ValidateStageProvenance(req Request, outputDir string) error {
	if strings.TrimSpace(req.MetadataDir) == "" {
		return nil // direct library callers retain the pre-metadata compatibility path
	}
	fingerprint, err := makeStageFingerprint(req, outputDir)
	if err != nil {
		return err
	}
	provenanceMu.Lock()
	doc, loadErr := loadProvenance(provenancePath(req.MetadataDir))
	provenanceMu.Unlock()
	if loadErr != nil {
		return loadErr
	}
	record, ok := doc.Outputs[fingerprint.OutputDir]
	if !ok && len(doc.Outputs) > 0 {
		// Windows paths are case-insensitive; canonicalization alone does not
		// necessarily normalize drive-letter/path casing.
		for key, candidate := range doc.Outputs {
			if isSamePath(key, fingerprint.OutputDir) {
				record, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return errors.New("找不到当前 WAR 的抽取凭据，请重新抽取")
	}
	if req.StageToken != "" && req.StageToken != record.Token {
		return errors.New("WAR 抽取阶段凭据已失效，请重新抽取")
	}
	if !isSamePath(record.OutputDir, fingerprint.OutputDir) || !isSamePath(record.ProjectDir, fingerprint.ProjectDir) ||
		record.PackageName != fingerprint.PackageName || record.ManifestSHA256 != fingerprint.ManifestSHA256 ||
		record.AutoPair != fingerprint.AutoPair || record.PackType != fingerprint.PackType ||
		record.BatchBaseDir != fingerprint.BatchBaseDir || record.ChmodMode != fingerprint.ChmodMode {
		return errors.New("WAR 对应的抽取配置已变化，请重新抽取")
	}
	return nil
}

func loadProvenance(path string) (provenanceDocument, error) {
	doc := provenanceDocument{Version: 1, Outputs: make(map[string]StageProvenance)}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("读取抽取凭据失败: %w", err)
	}
	if len(raw) == 0 {
		return doc, errors.New("抽取凭据文件为空或已损坏（文件未覆盖）")
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, fmt.Errorf("解析抽取凭据失败（文件未覆盖）: %w", err)
	}
	if doc.Version != 1 || doc.Outputs == nil {
		return doc, errors.New("不支持的抽取凭据文件版本（文件未覆盖）")
	}
	return doc, nil
}

func writeProvenance(path string, doc provenanceDocument) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化抽取凭据失败: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建抽取凭据目录失败: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".waspack-provenance-*.tmp")
	if err != nil {
		return fmt.Errorf("创建抽取凭据临时文件失败: %w", err)
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
		return fmt.Errorf("原子替换抽取凭据失败: %w", err)
	}
	ok = true
	return nil
}
