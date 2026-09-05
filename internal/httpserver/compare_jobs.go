package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

const (
	// A compare job owns remote connections and potentially many goroutines.
	// Bound it even when the browser disappears so those resources are not
	// retained forever.
	compareJobTimeout  = 15 * time.Minute
	compareJobTTL      = 30 * time.Minute
	compareJobLimit    = 8
	compareStartLimit  = 16
	compareStartWindow = time.Minute
)

type compareJob struct {
	mu         sync.RWMutex
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Phase      string             `json:"phase"`
	Current    int                `json:"current"`
	Total      int                `json:"total"`
	Message    string             `json:"message,omitempty"`
	Result     *compareScanResult `json:"result,omitempty"`
	SyncResult *compareSyncResult `json:"sync_result,omitempty"`
	Error      string             `json:"error,omitempty"`
	Started    time.Time          `json:"started_at"`
	Updated    time.Time          `json:"updated_at"`
	cancel     context.CancelFunc
}

type compareJobView struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Phase      string             `json:"phase"`
	Current    int                `json:"current"`
	Total      int                `json:"total"`
	Message    string             `json:"message,omitempty"`
	Result     *compareScanResult `json:"result,omitempty"`
	SyncResult *compareSyncResult `json:"sync_result,omitempty"`
	Error      string             `json:"error,omitempty"`
	Started    time.Time          `json:"started_at"`
	Updated    time.Time          `json:"updated_at"`
}

func (j *compareJob) view() compareJobView {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return compareJobView{ID: j.ID, Status: j.Status, Phase: j.Phase, Current: j.Current, Total: j.Total, Message: j.Message, Result: j.Result, SyncResult: j.SyncResult, Error: j.Error, Started: j.Started, Updated: j.Updated}
}

func (j *compareJob) progress(phase string, current, total int, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Phase, j.Current, j.Total, j.Message, j.Updated = phase, current, total, message, time.Now()
}

func (j *compareJob) release() {
	if j.cancel != nil {
		j.cancel()
	}
}

type compareJobManager struct {
	mu     sync.RWMutex
	jobs   map[string]*compareJob
	starts []time.Time
}

func newCompareJobManager() *compareJobManager {
	return &compareJobManager{jobs: make(map[string]*compareJob)}
}

func (m *compareJobManager) tryCreate(parent context.Context) (*compareJob, context.Context, error) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	active := 0
	for key, old := range m.jobs {
		view := old.view()
		if view.Updated.Before(now.Add(-compareJobTTL)) {
			delete(m.jobs, key)
			continue
		}
		if view.Status == "running" {
			active++
		}
	}
	if active >= compareJobLimit {
		return nil, nil, errors.New("compare 并发任务已达上限，请稍后重试")
	}
	cutoff := now.Add(-compareStartWindow)
	kept := m.starts[:0]
	for _, started := range m.starts {
		if started.After(cutoff) {
			kept = append(kept, started)
		}
	}
	m.starts = kept
	if len(m.starts) >= compareStartLimit {
		return nil, nil, errors.New("compare 任务创建过于频繁，请稍后重试")
	}
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return nil, nil, errors.New("无法创建 compare 任务标识")
	}
	id := hex.EncodeToString(raw)
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, compareJobTimeout)
	job := &compareJob{ID: id, Status: "running", Phase: "starting", Started: now, Updated: now, cancel: cancel}
	m.jobs[id] = job
	m.starts = append(m.starts, now)
	return job, ctx, nil
}

func (m *compareJobManager) get(id string) (*compareJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	return job, ok
}
