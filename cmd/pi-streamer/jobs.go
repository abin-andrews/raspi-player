// Bridges internal/jobs.Manager to internal/api.Jobs — the same "own copy
// of the shape, no direct import" pattern internal/api already uses for
// Bucket/Art, so that package stays decoupled from the concrete
// job-tracking implementation.
package main

import (
	"pi-streamer/internal/api"
	"pi-streamer/internal/jobs"
)

type jobsAdapter struct {
	mgr *jobs.Manager
}

func (j *jobsAdapter) List() []api.Job {
	list := j.mgr.List()
	out := make([]api.Job, len(list))
	for i, job := range list {
		out[i] = api.Job{
			ID:        job.ID,
			Name:      job.Name,
			Status:    string(job.Status),
			Total:     job.Total,
			Done:      job.Done,
			Error:     job.Error,
			StartedAt: job.StartedAt,
			EndedAt:   job.EndedAt,
		}
	}
	return out
}
