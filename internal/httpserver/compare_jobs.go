package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
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

type compareJobManager struct {
	mu   sync.RWMutex
	jobs map[string]*compareJob
}

func newCompareJobManager() *compareJobManager {
	return &compareJobManager{jobs: make(map[string]*compareJob)}
}

func (m *compareJobManager) create(parent context.Context) (*compareJob, context.Context) {
	raw := make([]byte, 12)
	_, _ = rand.Read(raw)
	id := hex.EncodeToString(raw)
	ctx, cancel := context.WithCancel(parent)
	now := time.Now()
	job := &compareJob{ID: id, Status: "running", Phase: "starting", Started: now, Updated: now, cancel: cancel}
	m.mu.Lock()
	m.jobs[id] = job
	// Opportunistic cleanup keeps completed job results bounded without another ticker.
	for key, old := range m.jobs {
		if key != id && old.view().Updated.Before(now.Add(-30*time.Minute)) {
			delete(m.jobs, key)
		}
	}
	m.mu.Unlock()
	return job, ctx
}

func (m *compareJobManager) get(id string) (*compareJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	return job, ok
}
