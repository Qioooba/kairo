package httpserver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWASPackHistoryAPIListDetailDeleteAndRebuild(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	root := t.TempDir()
	project := filepath.Join(root, "工程")
	output := filepath.Join(root, "output")
	rebuiltOutput := filepath.Join(root, "rebuild-output")
	if err := os.MkdirAll(filepath.Join(project, "WebRoot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "WebRoot", "a.jsp"), []byte("<%-- demo --%>"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{
		"project_dir": project, "output_dir": output, "package_name": "Demo",
		"manifest": "./a.jsp\n", "auto_pair": true, "output_policy": "fail",
	}
	resp := doRequest(srv, "POST", "/api/waspack/build", body)
	if resp.Code != 200 {
		t.Fatalf("build status=%d body=%s", resp.Code, resp.Body.String())
	}
	var build map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &build); err != nil || build["tar_file"] == nil {
		t.Fatalf("build response=%v err=%v", build, err)
	}
	listResp := doRequest(srv, "GET", "/api/waspack/history?limit=10&operation=build", nil)
	if listResp.Code != 200 {
		t.Fatalf("history list status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].ID == "" {
		t.Fatalf("history list=%s err=%v", listResp.Body.String(), err)
	}
	if containsJSON(listResp.Body.Bytes(), "./a.jsp") {
		t.Fatal("history list must not embed the full manifest")
	}
	id := list.Items[0].ID
	detailResp := doRequest(srv, "GET", "/api/waspack/history/"+id, nil)
	if detailResp.Code != 200 || !containsJSON(detailResp.Body.Bytes(), "./a.jsp") {
		t.Fatalf("history detail status=%d body=%s", detailResp.Code, detailResp.Body.String())
	}
	rebuildResp := doRequest(srv, "POST", "/api/waspack/history/"+id+"/rebuild", map[string]any{
		"output_dir": rebuiltOutput, "include_zip": true,
	})
	if rebuildResp.Code != 200 {
		t.Fatalf("rebuild status=%d body=%s", rebuildResp.Code, rebuildResp.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rebuiltOutput, "Demo.tar")); err != nil {
		t.Fatalf("rebuilt tar missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rebuiltOutput, "Demo.zip")); err != nil {
		t.Fatalf("rebuilt zip missing: %v", err)
	}
	if !containsJSON(rebuildResp.Body.Bytes(), id) {
		t.Fatalf("rebuild response should link source id: %s", rebuildResp.Body.String())
	}
	deleteResp := doRequest(srv, "DELETE", "/api/waspack/history/"+id, nil)
	if deleteResp.Code != 200 {
		t.Fatalf("delete status=%d body=%s", deleteResp.Code, deleteResp.Body.String())
	}
}

func TestWASPackHistoryRecordsFailedAttempt(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(project, "WebRoot"), 0o755); err != nil {
		t.Fatal(err)
	}
	resp := doRequest(srv, "POST", "/api/waspack/build", map[string]any{
		"project_dir": project, "output_dir": filepath.Join(root, "out"),
		"package_name": "Demo", "manifest": "./does-not-exist.jsp",
	})
	if resp.Code != 400 {
		t.Fatalf("failed build status=%d body=%s", resp.Code, resp.Body.String())
	}
	listResp := doRequest(srv, "GET", "/api/waspack/history?status=failure&operation=build", nil)
	if listResp.Code != 200 || !containsJSON(listResp.Body.Bytes(), "failure") {
		t.Fatalf("failed history missing status=%d body=%s", listResp.Code, listResp.Body.String())
	}
}

func TestWASPackHistoryRebuildReplaceNeedsFreshConfirmation(t *testing.T) {
	srv, _, _, _ := newTestServer(t)
	root := t.TempDir()
	project := filepath.Join(root, "project")
	output := filepath.Join(root, "output")
	if err := os.MkdirAll(filepath.Join(project, "WebRoot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "WebRoot", "a.jsp"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"project_dir": project, "output_dir": output, "package_name": "Demo", "manifest": "./a.jsp"}
	if got := doRequest(srv, "POST", "/api/waspack/build", body); got.Code != 200 {
		t.Fatalf("build status=%d body=%s", got.Code, got.Body.String())
	}
	listResp := doRequest(srv, "GET", "/api/waspack/history?operation=build", nil)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list=%s err=%v", listResp.Body.String(), err)
	}
	resp := doRequest(srv, "POST", "/api/waspack/history/"+list.Items[0].ID+"/rebuild", map[string]any{
		"output_dir": output, "output_policy": "replace",
	})
	if resp.Code != 400 || !containsJSON(resp.Body.Bytes(), "不会继承历史确认") {
		t.Fatalf("replace without fresh confirmation status=%d body=%s", resp.Code, resp.Body.String())
	}
}

func containsJSON(raw []byte, needle string) bool {
	return bytes.Contains(raw, []byte(needle))
}
