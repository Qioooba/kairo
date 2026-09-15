package httpserver

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestT075_CompareJobsConcurrencyAndRetentionLimits(t *testing.T) {
	mgr := newCompareJobManager()

	// 1. 测试并发限制：compareJobLimit = 8
	var jobs []*compareJob

	for i := 0; i < compareJobLimit; i++ {
		job, ctx, err := mgr.tryCreate(context.Background())
		if err != nil {
			t.Fatalf("failed to create job %d: %v", i, err)
		}
		jobs = append(jobs, job)
		_ = ctx
	}

	// 第 9 个并发任务应触发上限拒绝
	_, _, err := mgr.tryCreate(context.Background())
	if err == nil || !strings.Contains(err.Error(), "并发任务已达上限") {
		t.Fatalf("expected concurrency limit error, got: %v", err)
	}

	// 2. 取消前 4 个任务并标记为已结束
	for i := 0; i < 4; i++ {
		jobs[i].mu.Lock()
		jobs[i].Status = "cancelled"
		jobs[i].Updated = time.Now()
		jobs[i].mu.Unlock()
		jobs[i].release()
	}

	// 现在活跃任务为 4 个，应能再次创建新任务
	job9, _, err := mgr.tryCreate(context.Background())
	if err != nil {
		t.Fatalf("expected success after active count reduced, got: %v", err)
	}
	jobs = append(jobs, job9)

	// 3. 测试完成态任务保留上限（compareJobRetainedLimit = 16）与淘汰机制
	// 将目前所有任务置为 completed
	for _, j := range jobs {
		j.mu.Lock()
		j.Status = "completed"
		j.Updated = time.Now()
		j.mu.Unlock()
		j.release()
	}

	// 填满至 16 个 completed 任务
	mgr.mu.Lock()
	mgr.starts = nil // 重置启动频率窗口以便集中测试淘汰
	mgr.mu.Unlock()

	for len(mgr.jobs) < compareJobRetainedLimit {
		j, _, createErr := mgr.tryCreate(context.Background())
		if createErr != nil {
			break
		}
		j.mu.Lock()
		j.Status = "completed"
		j.Updated = time.Now()
		j.mu.Unlock()
		j.release()
	}

	mgr.mu.Lock()
	initialCount := len(mgr.jobs)
	mgr.starts = nil // 再次清空频率限制
	mgr.mu.Unlock()

	// 再创建 1 个，此时应自动淘汰最旧的完成任务，保持总数不超过 compareJobRetainedLimit
	extraJob, _, err := mgr.tryCreate(context.Background())
	if err != nil {
		t.Fatalf("failed to create job when evicting oldest: %v", err)
	}
	extraJob.release()

	mgr.mu.Lock()
	finalCount := len(mgr.jobs)
	mgr.mu.Unlock()

	t.Logf("T075 Task retention: initial=%d, final=%d (max retained limit=%d)", initialCount, finalCount, compareJobRetainedLimit)
	if finalCount > compareJobRetainedLimit {
		t.Errorf("retained jobs exceeded limit: got %d, max %d", finalCount, compareJobRetainedLimit)
	}
}

func TestT075_CompareJobsCancellationResponsiveness(t *testing.T) {
	mgr := newCompareJobManager()

	job, ctx, err := mgr.tryCreate(context.Background())
	if err != nil {
		t.Fatalf("tryCreate failed: %v", err)
	}

	start := time.Now()
	job.release() // 释放并取消 ctx

	select {
	case <-ctx.Done():
		elapsed := time.Since(start)
		t.Logf("Compare job cancellation observed in %v", elapsed)
		if elapsed > 50*time.Millisecond {
			t.Errorf("cancellation took %v, want < 50ms", elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("context was not cancelled within 500ms")
	}
}
