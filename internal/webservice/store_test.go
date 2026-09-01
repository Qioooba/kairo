package webservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kairo/internal/testutil"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewStore(dir)
}

func TestStore_TemplatesCRUD(t *testing.T) {
	s := newTestStore(t)

	tpl := Template{
		Name: "查询客户", Group: "信贷", Endpoint: "http://x/",
		Operation: "customerQuery", SOAPAction: "http://x/q",
		Body: "<x/>", Encoding: "UTF-8", TimeoutMs: 5000,
	}
	saved, replaced, err := s.SaveTemplate(tpl)
	if err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	if replaced {
		t.Errorf("first save should not replace")
	}
	if saved.ID == "" {
		t.Errorf("ID should be assigned")
	}

	list, err := s.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 template, got %d", len(list))
	}

	// 同 group+name 覆盖
	saved.Body = "<y/>"
	saved2, replaced2, err := s.SaveTemplate(saved)
	if err != nil {
		t.Fatalf("SaveTemplate(2): %v", err)
	}
	if !replaced2 {
		t.Errorf("same group+name should replace")
	}
	list, _ = s.ListTemplates()
	if len(list) != 1 {
		t.Errorf("after replace want 1, got %d", len(list))
	}
	if list[0].Body != "<y/>" {
		t.Errorf("body not updated: %q", list[0].Body)
	}

	// 删除
	ok, err := s.DeleteTemplate(saved2.ID)
	if err != nil || !ok {
		t.Fatalf("DeleteTemplate: ok=%v err=%v", ok, err)
	}
	list, _ = s.ListTemplates()
	if len(list) != 0 {
		t.Errorf("after delete want 0, got %d", len(list))
	}
}

func TestStore_HistoryLimit(t *testing.T) {
	s := newTestStore(t)
	// 写入 MaxHistoryEntries + 50 条
	total := MaxHistoryEntries + 50
	for i := 0; i < total; i++ {
		_, err := s.AppendHistory(HistoryEntry{
			Endpoint: "http://x/", Operation: "op", RequestBody: "<x/>",
			StatusCode: 200, Success: true,
		})
		if err != nil {
			t.Fatalf("AppendHistory %d: %v", i, err)
		}
	}
	list, err := s.ListHistory()
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(list) != MaxHistoryEntries {
		t.Errorf("history count = %d, want %d (capped)", len(list), MaxHistoryEntries)
	}
	// 清空
	if err := s.ClearHistory(); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	list, _ = s.ListHistory()
	if len(list) != 0 {
		t.Errorf("after clear want 0, got %d", len(list))
	}
}

func TestStore_ProjectsCRUD(t *testing.T) {
	s := newTestStore(t)
	p := WSDLProject{Name: "p1", Source: "url", SourceURL: "http://x/?wsdl", Operations: []Operation{{Name: "op1"}}}
	saved, err := s.SaveProject(p)
	if err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	if saved.ID == "" {
		t.Fatalf("SaveProject 返回的 ID 不应为空")
	}
	list, _ := s.ListProjects()
	if len(list) != 1 {
		t.Fatalf("want 1 project, got %d", len(list))
	}
	got, ok, _ := s.GetProject(list[0].ID)
	if !ok {
		t.Fatalf("GetProject not found")
	}
	if got.Name != "p1" {
		t.Errorf("name = %q", got.Name)
	}
	ok, _ = s.DeleteProject(list[0].ID)
	if !ok {
		t.Errorf("DeleteProject should succeed")
	}
	list, _ = s.ListProjects()
	if len(list) != 0 {
		t.Errorf("after delete want 0")
	}
}

func TestStore_FilesWrittenAtomic(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	_, _, _ = s.SaveTemplate(Template{Name: "t", Group: "g"})
	// 文件应存在且权限 0600
	path := filepath.Join(dir, "soap_templates.json")
	testutil.ReadPrivateFile(t, path)
}

func TestStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	s1 := NewStore(dir)
	_, _, _ = s1.SaveTemplate(Template{Name: "t1", Group: "g1"})
	// 新实例从同一目录加载
	s2 := NewStore(dir)
	list, err := s2.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(list) != 1 || list[0].Name != "t1" {
		t.Errorf("persistence failed: %+v", list)
	}
}

func TestStoreReadsLegacyArrayAndNeverOverwritesFutureEnvelope(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "soap_templates.json")
	legacy := []byte(`[{"id":"legacy","name":"old","group":"g"}]`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := NewStore(dir).ListTemplates()
	if err != nil || len(list) != 1 || list[0].ID != "legacy" {
		t.Fatalf("legacy array was not preserved: list=%+v err=%v", list, err)
	}

	future := []byte(`{"version":99,"items":[],"future":"keep"}`)
	if err := os.WriteFile(path, future, 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(dir)
	if _, _, err := store.SaveTemplate(Template{Name: "new", Group: "g"}); err == nil {
		t.Fatal("future webservice format must reject writes")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(future) {
		t.Fatal("future webservice file was overwritten")
	}
}

// ---------- Mock 测试 ----------

func TestMockRegistry_RouteAndRecord(t *testing.T) {
	s := newTestStore(t)
	reg := NewMockRegistry(s)
	_, _, _ = s.SaveMock(MockConfig{
		Name: "客户查询Mock", Path: "/mock/customerQuery",
		StatusCode: 200, Enabled: true, Body: `<resp><custName>Mock张三</custName></resp>`,
	})
	if err := reg.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	m, ok := reg.Lookup("/mock/customerQuery")
	if !ok {
		t.Fatalf("mock not registered")
	}
	if m.Body == "" {
		t.Errorf("mock body empty")
	}

	// 用 httptest 调用 ServeHTTP
	body := reg.serveForTest(t, "/mock/customerQuery", `<soapenv:Envelope/>`)
	if !strings.Contains(body, "Mock张三") {
		t.Errorf("mock response wrong: %s", body)
	}

	// 请求异步落盘，等队列排空后再查
	reg.FlushRecords()
	recs, err := s.ListMockRecords()
	if err != nil {
		t.Fatalf("ListMockRecords: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	if recs[0].Path != "/mock/customerQuery" {
		t.Errorf("record path = %q", recs[0].Path)
	}
}

func TestMockRegistry_Delay(t *testing.T) {
	s := newTestStore(t)
	reg := NewMockRegistry(s)
	_, _, _ = s.SaveMock(MockConfig{
		Name: "慢Mock", Path: "/mock/slow", Enabled: true, DelayMs: 200, Body: `<r/>`,
	})
	_ = reg.Reload()

	start := time.Now()
	_ = reg.serveForTest(t, "/mock/slow", "")
	elapsed := time.Since(start)
	if elapsed < 180*time.Millisecond {
		t.Errorf("delay not applied: elapsed=%v", elapsed)
	}
	reg.FlushRecords()
}

func TestMockRegistry_NotFound(t *testing.T) {
	s := newTestStore(t)
	reg := NewMockRegistry(s)
	_ = reg.Reload()
	body := reg.serveForTestStatus(t, "/mock/nope", "", 404)
	if !strings.Contains(body, "未找到 mock") {
		t.Errorf("404 body wrong: %s", body)
	}
	reg.FlushRecords()
}

func TestMockRegistry_DisabledNotServed(t *testing.T) {
	s := newTestStore(t)
	reg := NewMockRegistry(s)
	_, _, _ = s.SaveMock(MockConfig{
		Name: "禁用", Path: "/mock/off", Enabled: false, Body: `<r/>`,
	})
	_ = reg.Reload()
	if _, ok := reg.Lookup("/mock/off"); ok {
		t.Errorf("disabled mock should not be registered")
	}
}

func TestMockRecordsLimit(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < MaxMockRecords+10; i++ {
		_ = s.AppendMockRecord(MockRequestRecord{Path: "/mock/x", Body: "b"})
	}
	recs, _ := s.ListMockRecords()
	if len(recs) != MaxMockRecords {
		t.Errorf("records = %d, want %d", len(recs), MaxMockRecords)
	}
}

// ---------- 辅助：通过 ServeHTTP 驱动 mock ----------

func (r *MockRegistry) serveForTest(t *testing.T, path, body string) string {
	t.Helper()
	return r.serveForTestStatus(t, path, body, 200)
}

func (r *MockRegistry) serveForTestStatus(t *testing.T, path, body string, wantStatus int) string {
	t.Helper()
	req := mustNewRequest("POST", path, body)
	rr := mustServe(r, req)
	if wantStatus > 0 && rr.Code != wantStatus {
		t.Errorf("status = %d, want %d", rr.Code, wantStatus)
	}
	return rr.Body.String()
}
