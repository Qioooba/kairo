// 集成测试 - 真实调 mock-sponsor-server, 验证 Go 端 sponsor.FetchLeaderboard() 全流程
//
// 运行方法:
//   1. 启动 mock server:  go run ./cmd/mock-sponsor-server -addr :18095 -auth TEST_TOKEN_123
//   2. 运行本测试:        go test -tags=integration -v ./internal/sponsor/
//
//go:build integration

package sponsor

import (
	"testing"
	"time"
)

func TestIntegration_FetchLeaderboard_Success(t *testing.T) {
	// 指向 mock server
	Primary = "http://localhost:18095/credit/httpInterface"
	Secondary = ""
	BasicAuthHeader = "TEST_TOKEN_123"
	DefaultTimeout = 2 * time.Second

	// 重置 mock 回预置数据 (假设 mock 已启动, 否则 panic)
	// (这里不调 reset, 假设 mock 刚启动状态)

	lr, err := FetchLeaderboard()
	if err != nil {
		t.Fatalf("FetchLeaderboard: %v", err)
	}
	if !lr.OK {
		t.Fatalf("lr.OK 应为 true, got=false, err=%s", lr.Error)
	}
	if len(lr.Entries) == 0 {
		t.Fatal("Entries 应有数据, got=0")
	}

	// 验证排序 (rank 1 应该是 total 最高的)
	if lr.Entries[0].Total < lr.Entries[1].Total {
		t.Errorf("rank 1 (%d) total 应 >= rank 2 (%d)",
			lr.Entries[0].Total, lr.Entries[1].Total)
	}
	if lr.Entries[0].Rank != 1 {
		t.Errorf("第 1 名 rank 应为 1, got=%d", lr.Entries[0].Rank)
	}

	// 验证字段 (mock 第 1 条是 张三, cotti=4 lucky=4 milktea=1)
	if lr.Entries[0].RealName != "张三" {
		t.Errorf("rank 1 应是张三, got=%q", lr.Entries[0].RealName)
	}
	if lr.Entries[0].Cotti != 4 {
		t.Errorf("张三 cotti 应为 4, got=%d", lr.Entries[0].Cotti)
	}
	t.Logf("rank 1: %s (cotti=%d lucky=%d milktea=%d total=%d)",
		lr.Entries[0].RealName, lr.Entries[0].Cotti, lr.Entries[0].Lucky,
		lr.Entries[0].Milktea, lr.Entries[0].Total)
}

// TestIntegration_FetchLeaderboard_BadAuth 验证 mock server 在 auth 错时返 401
func TestIntegration_FetchLeaderboard_BadAuth(t *testing.T) {
	Primary = "http://localhost:18095/credit/httpInterface"
	Secondary = ""
	BasicAuthHeader = "WRONG_AUTH_TOKEN"
	DefaultTimeout = 2 * time.Second

	_, err := FetchLeaderboard()
	if err == nil {
		t.Fatal("Auth 错应返 err")
	}
	t.Logf("Auth 错时返: %v", err)
}
