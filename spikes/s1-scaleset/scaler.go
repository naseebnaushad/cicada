package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/actions/scaleset"
)

// jobAcquirer claims jobs GitHub offers to this scale set.
type jobAcquirer interface {
	AcquireJobs(ctx context.Context, requestIDs []int64) ([]int64, error)
}

// jitIssuer creates the single-use configuration a runner starts with.
type jitIssuer interface {
	GenerateJitRunnerConfig(ctx context.Context, setting *scaleset.RunnerScaleSetJitRunnerSetting, scaleSetID int) (*scaleset.RunnerScaleSetJitRunnerConfig, error)
}

// backend starts and removes runners. Start must not block for the life of the runner.
type backend interface {
	Start(ctx context.Context, name, jitConfig string) error
	Remove(ctx context.Context, name string) error
}

// scaler turns scale set messages into runners. It is the spike's version of the
// scaler use case; the listener never calls Scale concurrently, but runner exits arrive
// from other goroutines, so the runner set is locked.
type scaler struct {
	log        *slog.Logger
	jobs       jobAcquirer
	jit        jitIssuer
	backend    backend
	scaleSetID int
	maxRunners int
	newName    func() string

	mu      sync.Mutex
	runners map[string]struct{}
}

// Scale handles one poll result. A nil message means the long poll ended with no news.
func (s *scaler) Scale(ctx context.Context, msg *scaleset.RunnerScaleSetMessage) error {
	if msg == nil {
		return nil
	}

	// Acquire first so jobs are claimed as early as possible. Acquiring twice is
	// harmless, which matters because an unacknowledged message is redelivered.
	if len(msg.JobAvailableMessages) > 0 {
		ids := make([]int64, 0, len(msg.JobAvailableMessages))
		for _, job := range msg.JobAvailableMessages {
			ids = append(ids, job.RunnerRequestID)
			s.log.Info("job available", "repo", job.OwnerName+"/"+job.RepositoryName, "job", job.JobDisplayName, "labels", job.RequestLabels)
		}
		if _, err := s.jobs.AcquireJobs(ctx, ids); err != nil {
			return fmt.Errorf("acquire jobs: %w", err)
		}
	}
	for _, job := range msg.JobAssignedMessages {
		s.log.Info("job assigned to this scale set", "repo", job.OwnerName+"/"+job.RepositoryName, "job", job.JobDisplayName)
	}
	for _, job := range msg.JobStartedMessages {
		s.log.Info("job started", "runner", job.RunnerName, "job", job.JobDisplayName)
	}
	for _, job := range msg.JobCompletedMessages {
		s.log.Info("job completed", "runner", job.RunnerName, "job", job.JobDisplayName, "result", job.Result)
		// The runner exits by itself after one job; removing it again makes sure
		// nothing is left behind if it hangs.
		if s.forget(job.RunnerName) {
			if err := s.backend.Remove(ctx, job.RunnerName); err != nil {
				s.log.Warn("remove runner", "runner", job.RunnerName, "error", err)
			}
		}
	}

	if msg.Statistics == nil {
		return nil
	}
	st := msg.Statistics
	s.log.Info("statistics", "available", st.TotalAvailableJobs, "assigned", st.TotalAssignedJobs,
		"running", st.TotalRunningJobs, "registered", st.TotalRegisteredRunners, "idle", st.TotalIdleRunners)

	want := min(s.maxRunners, st.TotalAssignedJobs)
	for s.count() < want {
		if err := s.startRunner(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (s *scaler) startRunner(ctx context.Context) error {
	name := s.newName()
	jit, err := s.jit.GenerateJitRunnerConfig(ctx, &scaleset.RunnerScaleSetJitRunnerSetting{Name: name}, s.scaleSetID)
	if err != nil {
		return fmt.Errorf("issue runner configuration: %w", err)
	}
	// Track the runner before starting it, so an exit that races with Start is not lost.
	s.mu.Lock()
	s.runners[name] = struct{}{}
	s.mu.Unlock()
	if err := s.backend.Start(ctx, name, jit.EncodedJITConfig); err != nil {
		s.forget(name)
		return fmt.Errorf("start runner %s: %w", name, err)
	}
	s.log.Info("runner started", "runner", name)
	return nil
}

// runnerExited is called by the backend when a runner's process ends for any reason.
func (s *scaler) runnerExited(name string) {
	if s.forget(name) {
		s.log.Info("runner exited", "runner", name)
	}
}

// forget drops a runner from the set and reports whether it was there.
func (s *scaler) forget(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.runners[name]
	delete(s.runners, name)
	return ok
}

func (s *scaler) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runners)
}

// shutdown removes every runner still tracked.
func (s *scaler) shutdown(ctx context.Context) {
	s.mu.Lock()
	names := make([]string, 0, len(s.runners))
	for name := range s.runners {
		names = append(names, name)
	}
	clear(s.runners)
	s.mu.Unlock()
	for _, name := range names {
		if err := s.backend.Remove(ctx, name); err != nil {
			s.log.Warn("remove runner", "runner", name, "error", err)
		}
	}
}
