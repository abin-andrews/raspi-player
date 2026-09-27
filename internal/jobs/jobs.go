// Package jobs tracks long-running background work (e.g. warming the
// album art cache across a whole library) so it's visible and reportable
// instead of a silent fire-and-forget goroutine — a personal-scale
// equivalent of a job queue: no persistence, no retries, no distribution,
// just "what's running right now, and how far along is it."
package jobs

import (
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// Status is a Job's current state.
type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Job is a snapshot of one background job's state. Total/Done are 0 if the
// job never reported a total (an indeterminate, "still working" job).
type Job struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Total     int       `json:"total,omitempty"`
	Done      int       `json:"done,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitempty"`
}

// Manager tracks jobs in memory. The zero value is not usable; use New.
type Manager struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	nextID   int
	onChange func([]Job) // called (with the full current list) after every state change, if set
	maxKept  int         // caps how many finished jobs are retained, oldest dropped first
}

// New returns a Manager. onChange, if non-nil, is called after every job
// state change with a snapshot of every currently-tracked job (running or
// finished, up to maxKept most recent) — e.g. to push it out over a
// WebSocket, mirroring how bucket-download progress is already pushed.
// maxKept <= 0 defaults to 20.
func New(onChange func([]Job), maxKept int) *Manager {
	if maxKept <= 0 {
		maxKept = 20
	}
	return &Manager{jobs: make(map[string]*Job), onChange: onChange, maxKept: maxKept}
}

// Handle lets a running job report progress and completion. Passed to the
// fn given to Start.
type Handle struct {
	m  *Manager
	id string
}

// SetTotal records how many units of work this job expects to do, for a
// "N of M" style progress display. Optional — a job that never calls this
// just shows as "running" with no progress fraction.
func (h *Handle) SetTotal(total int) {
	h.m.update(h.id, func(j *Job) { j.Total = total })
}

// Advance increments Done by n (typically 1, once per unit of work
// completed).
func (h *Handle) Advance(n int) {
	h.m.update(h.id, func(j *Job) { j.Done += n })
}

// Start begins a new job named name, running fn in its own goroutine with
// its own panic recovery (a panic is recorded as a failure, not left to
// crash the daemon) and returns a snapshot of its initial state. fn
// reports progress via h and returning a non-nil error marks the job
// failed; returning nil marks it done.
func (m *Manager) Start(name string, fn func(h *Handle) error) Job {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("%d", m.nextID)
	job := &Job{ID: id, Name: name, Status: StatusRunning, StartedAt: time.Now()}
	m.jobs[id] = job
	snapshot := *job
	m.mu.Unlock()
	m.notify()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in job %s (%s): %v\n%s", id, name, r, debug.Stack())
				m.finish(id, fmt.Errorf("panic: %v", r))
			}
		}()
		err := fn(&Handle{m: m, id: id})
		m.finish(id, err)
	}()

	return snapshot
}

func (m *Manager) update(id string, mutate func(*Job)) {
	m.mu.Lock()
	if j, ok := m.jobs[id]; ok {
		mutate(j)
	}
	m.mu.Unlock()
	m.notify()
}

func (m *Manager) finish(id string, err error) {
	m.mu.Lock()
	if j, ok := m.jobs[id]; ok {
		j.EndedAt = time.Now()
		if err != nil {
			j.Status = StatusFailed
			j.Error = err.Error()
		} else {
			j.Status = StatusDone
		}
	}
	m.pruneLocked()
	m.mu.Unlock()
	m.notify()
}

// pruneLocked drops the oldest finished jobs once there are more than
// maxKept tracked in total, so a long-lived daemon doesn't accumulate an
// ever-growing map. Running jobs are never dropped.
func (m *Manager) pruneLocked() {
	if len(m.jobs) <= m.maxKept {
		return
	}
	type idAt struct {
		id string
		at time.Time
	}
	var finished []idAt
	for id, j := range m.jobs {
		if j.Status != StatusRunning {
			finished = append(finished, idAt{id, j.EndedAt})
		}
	}
	for len(m.jobs) > m.maxKept && len(finished) > 0 {
		oldestIdx := 0
		for i, f := range finished {
			if f.at.Before(finished[oldestIdx].at) {
				oldestIdx = i
			}
		}
		delete(m.jobs, finished[oldestIdx].id)
		finished = append(finished[:oldestIdx], finished[oldestIdx+1:]...)
	}
}

// List returns a snapshot of every currently-tracked job, most-recently-
// started first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked()
}

func (m *Manager) listLocked() []Job {
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.After(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (m *Manager) notify() {
	if m.onChange == nil {
		return
	}
	m.onChange(m.List())
}
