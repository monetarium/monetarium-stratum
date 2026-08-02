package mining

import (
	"bytes"
	"strconv"
	"sync"
)

// WorkReason enumerates why new work was received from the node.
const (
	// ReasonNewParent indicates the best block changed and a new block
	// template was created.  Miners should consider previous jobs stale.
	ReasonNewParent = "NewParent"

	// ReasonNewVotes indicates new votes were included in the template.
	ReasonNewVotes = "NewVotes"

	// ReasonNewTxns indicates new transactions were added to the template.
	ReasonNewTxns = "NewTxns"
)

// WorkManager holds the current job and a bounded registry of recent jobs so
// that submissions can be validated against the work they claim to solve.
type WorkManager struct {
	mu      sync.RWMutex
	current *Work
	jobs    map[string]*Work
	order   []string
	seq     uint64
	maxJobs int
}

// NewWorkManager creates a new work manager.  maxJobs bounds the number of
// retained jobs for submission validation.
func NewWorkManager(maxJobs int) *WorkManager {
	return &WorkManager{
		jobs:    make(map[string]*Work),
		maxJobs: maxJobs,
	}
}

// SetCurrent replaces the current work with new data received from the node
// and returns the new work along with a flag indicating whether it is a
// template for a new parent block (clean jobs).
func (m *WorkManager) SetCurrent(data []byte, reason string) (*Work, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ignore duplicate data for the same template.  This avoids generating
	// spurious jobs for timestamp-rolled work notifications.
	if m.current != nil && bytes.Equal(m.current.data[:], data) {
		return m.current, false
	}

	m.seq++
	jobID := strconv.FormatUint(m.seq, 10)
	work, err := NewWork(data, jobID)
	if err != nil {
		return m.current, false
	}

	m.current = work
	// Evict the oldest job if the registry is at capacity.
	if len(m.order) >= m.maxJobs {
		oldest := m.order[0]
		m.order = m.order[1:]
		delete(m.jobs, oldest)
	}
	m.order = append(m.order, work.jobID)
	m.jobs[work.jobID] = work

	return work, reason == ReasonNewParent
}

// Current returns the current work, or nil if none has been set.
func (m *WorkManager) Current() *Work {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// Job returns the work for the given job identifier.
func (m *WorkManager) Job(jobID string) (*Work, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	work, ok := m.jobs[jobID]
	return work, ok
}
