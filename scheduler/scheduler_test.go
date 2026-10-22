package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhawalhost/gokit/distlock"
)

func TestCron_Parse_Valid(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"standard_midnight", "0 0 * * *"},
		{"hourly_at_30", "30 * * * *"},
		{"step_minutes", "*/15 * * * *"},
		{"range_hours", "0 9-17 * * *"},
		{"list_days", "0 0 1,15 * *"},
		{"named_days", "0 12 * * MON,WED,FRI"},
		{"sunday_7", "0 0 * * 7"},
		{"sunday_0", "0 0 * * 0"},
		{"macro_yearly", "@yearly"},
		{"macro_annually", "@annually"},
		{"macro_monthly", "@monthly"},
		{"macro_weekly", "@weekly"},
		{"macro_daily", "@daily"},
		{"macro_midnight", "@midnight"},
		{"macro_hourly", "@hourly"},
		{"every_seconds", "@every 5s"},
		{"every_minutes", "@every 10m"},
		{"every_subsecond_clamped", "@every 100ms"},
		{"tz_cron_tz", "CRON_TZ=America/New_York 0 9 * * 1-5"},
		{"tz_tz_prefix", "TZ=UTC 0 12 * * *"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sch, err := Parse(tc.spec)
			if err != nil {
				t.Fatalf("unexpected error parsing %q: %v", tc.spec, err)
			}
			if sch == nil {
				t.Fatalf("expected non-nil schedule for %q", tc.spec)
			}
		})
	}
}

func TestCron_Parse_Invalid(t *testing.T) {
	tests := []struct {
		name string
		spec string
	}{
		{"empty", ""},
		{"too_few_fields", "0 0 * *"},
		{"too_many_fields", "0 0 * * * *"},
		{"bad_interval", "@every invalid"},
		{"missing_fields_after_tz", "CRON_TZ=America/New_York"},
		{"unknown_tz", "CRON_TZ=Fake/NonExistent 0 0 * * *"},
		{"minute_out_of_range", "60 * * * *"},
		{"hour_out_of_range", "* 24 * * *"},
		{"day_out_of_range", "* * 32 * *"},
		{"month_out_of_range", "* * * 13 *"},
		{"dow_out_of_range", "* * * * 8"},
		{"bad_range_order", "* 18-9 * * *"},
		{"bad_step_zero", "*/0 * * * *"},
		{"empty_list_part", "1,,2 * * * *"},
		{"bad_step_format", "*/1/2 * * * *"},
		{"bad_range_format", "1-2-3 * * * *"},
		{"non_numeric", "abc * * * *"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sch, err := Parse(tc.spec)
			if err == nil {
				t.Fatalf("expected error for %q, got schedule: %+v", tc.spec, sch)
			}
			if !errors.Is(err, ErrInvalidSpec) {
				t.Errorf("expected ErrInvalidSpec, got %v", err)
			}
		})
	}
}

func TestCron_Next(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("daily_midnight", func(t *testing.T) {
		sch, err := Parse("0 0 * * *")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		next := sch.Next(baseTime)
		expected := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})

	t.Run("hourly_advance", func(t *testing.T) {
		sch, err := Parse("30 * * * *")
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		next := sch.Next(baseTime)
		expected := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})

	t.Run("every_schedule", func(t *testing.T) {
		sch := Every(10 * time.Minute)
		next := sch.Next(baseTime)
		expected := baseTime.Add(10 * time.Minute)
		if !next.Equal(expected) {
			t.Errorf("expected %v, got %v", expected, next)
		}
	})
}

func TestScheduler_RegistrationAndLookup(t *testing.T) {
	s := New()

	// Invalid registration
	if _, err := s.Register("0 0 * * *", nil); err == nil {
		t.Error("expected error when registering nil Job")
	}
	if _, err := s.RegisterFunc("0 0 * * *", nil); err == nil {
		t.Error("expected error when registering nil func")
	}
	if _, err := s.Register("invalid spec", JobFunc(func(ctx context.Context) error { return nil })); err == nil {
		t.Error("expected error registering invalid spec")
	}

	// Valid registration
	id, err := s.RegisterFunc("@daily", func(ctx context.Context) error { return nil }, WithJobName("daily-cleanup"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if s.Count() != 1 {
		t.Errorf("expected count 1, got %d", s.Count())
	}

	snap, ok := s.Job(id)
	if !ok || snap.Name != "daily-cleanup" {
		t.Errorf("expected job with name 'daily-cleanup', got %+v", snap)
	}

	jobs := s.Jobs()
	if len(jobs) != 1 {
		t.Errorf("expected 1 job, got %d", len(jobs))
	}

	// Unregister
	if err := s.Unregister(id); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if s.Count() != 0 {
		t.Errorf("expected count 0 after unregister, got %d", s.Count())
	}
	if err := s.Unregister("non-existent"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}
	if _, ok := s.Job("non-existent"); ok {
		t.Error("expected false for nonexistent job")
	}
}

func TestScheduler_Execution(t *testing.T) {
	var runCount atomic.Int64

	s := New(WithTickInterval(10 * time.Millisecond))

	_, err := s.RegisterFunc("@every 20ms", func(ctx context.Context) error {
		runCount.Add(1)
		return nil
	}, WithJobName("counter"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.StartAsync(ctx)

	// Wait for at least 2 executions
	for i := 0; i < 50; i++ {
		if runCount.Load() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	_ = s.Stop(context.Background())

	if runCount.Load() < 2 {
		t.Errorf("expected at least 2 runs, got %d", runCount.Load())
	}
}

func TestScheduler_OverlapSkip(t *testing.T) {
	var executed atomic.Int64
	var skipped atomic.Int64

	s := New(
		WithTickInterval(10*time.Millisecond),
		WithHook(Hook{
			OnJobSkipped: func(snapshot JobSnapshot, reason string) {
				if reason == "overlap" {
					skipped.Add(1)
				}
			},
		}),
	)

	// Job runs for 60ms, but scheduled every 15ms with OverlapSkip
	_, err := s.RegisterFunc("@every 15ms", func(ctx context.Context) error {
		executed.Add(1)
		time.Sleep(60 * time.Millisecond)
		return nil
	}, WithJobOverlapPolicy(OverlapSkip))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.StartAsync(ctx)

	time.Sleep(100 * time.Millisecond)
	cancel()
	_ = s.Stop(context.Background())

	if skipped.Load() == 0 {
		t.Errorf("expected at least 1 skipped execution due to overlap, got %d", skipped.Load())
	}
}

func TestScheduler_PanicRecovery(t *testing.T) {
	var failed atomic.Bool

	s := New(
		WithHook(Hook{
			OnJobFailure: func(snapshot JobSnapshot, duration time.Duration, err error) {
				failed.Store(true)
			},
		}),
	)

	id, err := s.RegisterFunc("@daily", func(ctx context.Context) error {
		panic("simulated fatal panic")
	}, WithJobName("panicker"))
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	err = s.RunJob(context.Background(), id)
	if err == nil {
		t.Fatal("expected error from panicking job")
	}

	snap, _ := s.Job(id)
	if snap.State != StateFailed {
		t.Errorf("expected state StateFailed, got %v", snap.State)
	}
	if snap.FailureCount != 1 {
		t.Errorf("expected failure count 1, got %d", snap.FailureCount)
	}
	if !failed.Load() {
		t.Error("expected OnJobFailure hook to be called on panic")
	}
}

func TestScheduler_TimeoutAndRetries(t *testing.T) {
	var attempts atomic.Int64

	s := New()

	id, err := s.RegisterFunc("@daily", func(ctx context.Context) error {
		attempts.Add(1)
		return errors.New("transient database error")
	},
		WithJobTimeout(500*time.Millisecond),
		WithJobRetries(2, 5*time.Millisecond),
		WithJobMetadata(map[string]string{"env": "test"}),
	)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	err = s.RunJob(context.Background(), id)
	if err == nil {
		t.Fatal("expected job to return error")
	}

	// 1 initial run + 2 retries = 3 attempts
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}

	snap, _ := s.Job(id)
	if snap.Metadata["env"] != "test" {
		t.Errorf("expected metadata env=test, got %v", snap.Metadata)
	}
}

func TestScheduler_DistributedLocking(t *testing.T) {
	locker := distlock.NewInMemoryLocker()

	var pod1Runs atomic.Int64
	var pod2Runs atomic.Int64
	var lockSkips atomic.Int64

	hook := Hook{
		OnJobSkipped: func(snapshot JobSnapshot, reason string) {
			if reason == "locked_by_peer" {
				lockSkips.Add(1)
			}
		},
	}

	s1 := New(WithLocker(locker), WithHook(hook))
	s2 := New(WithLocker(locker), WithHook(hook))

	lockKey := "sync-report-job"
	lockTTL := 2 * time.Second

	jobFn1 := func(ctx context.Context) error {
		pod1Runs.Add(1)
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	jobFn2 := func(ctx context.Context) error {
		pod2Runs.Add(1)
		time.Sleep(50 * time.Millisecond)
		return nil
	}

	id1, err := s1.RegisterFunc("@daily", jobFn1, WithJobLock(lockKey, lockTTL))
	if err != nil {
		t.Fatalf("s1 register: %v", err)
	}

	id2, err := s2.RegisterFunc("@daily", jobFn2, WithJobLock(lockKey, lockTTL))
	if err != nil {
		t.Fatalf("s2 register: %v", err)
	}

	// Concurrently invoke RunJob on both schedulers
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = s1.RunJob(context.Background(), id1)
	}()

	go func() {
		defer wg.Done()
		_ = s2.RunJob(context.Background(), id2)
	}()

	wg.Wait()

	totalRuns := pod1Runs.Load() + pod2Runs.Load()
	if totalRuns != 1 {
		t.Errorf("expected exactly 1 pod to execute the locked job, got %d", totalRuns)
	}
	if lockSkips.Load() != 1 {
		t.Errorf("expected exactly 1 peer lock skip, got %d", lockSkips.Load())
	}
}

func TestScheduler_LifecycleErrors(t *testing.T) {
	s := New(WithTickInterval(20 * time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.StartAsync(ctx)
	time.Sleep(20 * time.Millisecond)

	// Second Start call should return ErrSchedulerRunning
	err := s.Start(ctx)
	if !errors.Is(err, ErrSchedulerRunning) {
		t.Errorf("expected ErrSchedulerRunning, got %v", err)
	}

	// RunJob with unknown ID
	if err := s.RunJob(ctx, "unknown"); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("expected ErrJobNotFound, got %v", err)
	}

	// Stop
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Redundant stop is safe no-op
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}
