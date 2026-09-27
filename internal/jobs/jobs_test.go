package jobs

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitForStatus(t *testing.T, m *Manager, id string, want Status) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, j := range m.List() {
			if j.ID == id && j.Status == want {
				return j
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for job %s to reach status %s", id, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartReturnsRunningJob(t *testing.T) {
	m := New(nil, 0)
	block := make(chan struct{})
	job := m.Start("test job", func(h *Handle) error {
		<-block
		return nil
	})
	defer close(block)

	if job.Status != StatusRunning {
		t.Errorf("Status = %q, want %q", job.Status, StatusRunning)
	}
	if job.Name != "test job" {
		t.Errorf("Name = %q, want %q", job.Name, "test job")
	}
	if job.StartedAt.IsZero() {
		t.Error("StartedAt is zero, want set")
	}
}

func TestStartMarksDoneOnSuccess(t *testing.T) {
	m := New(nil, 0)
	job := m.Start("test job", func(h *Handle) error { return nil })

	done := waitForStatus(t, m, job.ID, StatusDone)
	if done.Error != "" {
		t.Errorf("Error = %q, want empty on success", done.Error)
	}
	if done.EndedAt.IsZero() {
		t.Error("EndedAt is zero, want set")
	}
}

func TestStartMarksFailedOnError(t *testing.T) {
	m := New(nil, 0)
	job := m.Start("test job", func(h *Handle) error { return errors.New("boom") })

	failed := waitForStatus(t, m, job.ID, StatusFailed)
	if failed.Error != "boom" {
		t.Errorf("Error = %q, want %q", failed.Error, "boom")
	}
}

func TestStartRecoversFromPanic(t *testing.T) {
	m := New(nil, 0)
	job := m.Start("test job", func(h *Handle) error { panic("kaboom") })

	failed := waitForStatus(t, m, job.ID, StatusFailed)
	if failed.Error == "" {
		t.Error("Error is empty, want the recovered panic message recorded")
	}
}

func TestHandleSetTotalAndAdvance(t *testing.T) {
	m := New(nil, 0)
	started := make(chan *Handle)
	block := make(chan struct{})
	job := m.Start("test job", func(h *Handle) error {
		started <- h
		<-block
		return nil
	})
	defer close(block)

	h := <-started
	h.SetTotal(10)
	h.Advance(1)
	h.Advance(2)

	var got Job
	for _, j := range m.List() {
		if j.ID == job.ID {
			got = j
		}
	}
	if got.Total != 10 || got.Done != 3 {
		t.Errorf("Total/Done = %d/%d, want 10/3", got.Total, got.Done)
	}
}

func TestListOrdersMostRecentFirst(t *testing.T) {
	m := New(nil, 0)
	block := make(chan struct{})
	defer close(block)
	first := m.Start("first", func(h *Handle) error { <-block; return nil })
	time.Sleep(2 * time.Millisecond) // ensure a distinguishable StartedAt
	second := m.Start("second", func(h *Handle) error { <-block; return nil })

	list := m.List()
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Errorf("List() = %+v, want [second, first]", list)
	}
}

func TestOnChangeCalledOnEveryTransition(t *testing.T) {
	var calls atomic.Int64
	m := New(func(jobs []Job) { calls.Add(1) }, 0)
	job := m.Start("test job", func(h *Handle) error {
		h.SetTotal(1)
		h.Advance(1)
		return nil
	})
	waitForStatus(t, m, job.ID, StatusDone)

	// start (1) + SetTotal (1) + Advance (1) + finish (1) = at least 4
	if got := calls.Load(); got < 4 {
		t.Errorf("onChange called %d times, want at least 4", got)
	}
}

func TestPruneKeepsRunningJobsAndDropsOldestFinished(t *testing.T) {
	m := New(nil, 2)
	block := make(chan struct{})
	defer close(block)

	stillRunning := m.Start("running", func(h *Handle) error { <-block; return nil })
	first := m.Start("first finished", func(h *Handle) error { return nil })
	waitForStatus(t, m, first.ID, StatusDone)
	second := m.Start("second finished", func(h *Handle) error { return nil })
	waitForStatus(t, m, second.ID, StatusDone)

	// maxKept=2, but stillRunning must never be pruned regardless — so with
	// two finished jobs added after it, the oldest finished one (first)
	// should be dropped to make room, not the running one.
	list := m.List()
	var haveRunning, haveFirst, haveSecond bool
	for _, j := range list {
		switch j.ID {
		case stillRunning.ID:
			haveRunning = true
		case first.ID:
			haveFirst = true
		case second.ID:
			haveSecond = true
		}
	}
	if !haveRunning {
		t.Error("the still-running job was pruned — running jobs must never be dropped")
	}
	if !haveSecond {
		t.Error("the most recently finished job was pruned — should keep the newest")
	}
	if haveFirst {
		t.Error("the oldest finished job was kept — want it pruned to stay within maxKept")
	}
}
