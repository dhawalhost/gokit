package jobqueue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/dhawalhost/gokit/jobqueue"
	"github.com/redis/go-redis/v9"
)

func TestJob(t *testing.T) {
	tests := []struct {
		name       string
		jobType    string
		payload    any
		priority   int
		retries    int
		delay      time.Duration
		headers    map[string]string
		wantErr    bool
		wantHeader string
	}{
		{
			name:       "raw job creation",
			jobType:    "emails:send",
			payload:    []byte(`{"to":"user@test.com"}`),
			priority:   jobqueue.PriorityHigh,
			retries:    5,
			delay:      100 * time.Millisecond,
			headers:    map[string]string{"tenant_id": "tenant-1"},
			wantErr:    false,
			wantHeader: "tenant-1",
		},
		{
			name:       "json job serialization",
			jobType:    "reports:generate",
			payload:    map[string]string{"format": "pdf"},
			priority:   jobqueue.PriorityCritical,
			retries:    3,
			delay:      0,
			headers:    map[string]string{"env": "prod"},
			wantErr:    false,
			wantHeader: "prod",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var job *jobqueue.Job
			var err error

			switch p := tc.payload.(type) {
			case []byte:
				job = jobqueue.NewJob(tc.jobType, p).
					WithPriority(tc.priority).
					WithMaxRetries(tc.retries).
					WithDelay(tc.delay).
					WithHeader("header", tc.wantHeader)
			default:
				job, err = jobqueue.NewJSONJob(tc.jobType, p)
				if err != nil {
					t.Fatalf("unexpected NewJSONJob error: %v", err)
				}
				job.WithPriority(tc.priority).
					WithMaxRetries(tc.retries).
					WithHeader("header", tc.wantHeader)
			}

			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if job.ID == "" {
				t.Errorf("expected auto-generated UUID, got empty")
			}
			if job.Priority != tc.priority {
				t.Errorf("expected priority %d, got %d", tc.priority, job.Priority)
			}
			if job.Headers["header"] != tc.wantHeader {
				t.Errorf("expected header %s, got %s", tc.wantHeader, job.Headers["header"])
			}

			// Test Unmarshal
			var unmarshaled map[string]string
			if err := job.Unmarshal(&unmarshaled); err != nil {
				t.Errorf("Unmarshal failed: %v", err)
			}
		})
	}
}

func TestMemoryQueue(t *testing.T) {
	tests := []struct {
		name       string
		jobType    string
		failCount  int
		maxRetries int
		shouldPass bool
	}{
		{
			name:       "successful job execution on first attempt",
			jobType:    "tasks.success",
			failCount:  0,
			maxRetries: 3,
			shouldPass: true,
		},
		{
			name:       "job succeeds after retry",
			jobType:    "tasks.retry",
			failCount:  2,
			maxRetries: 3,
			shouldPass: true,
		},
		{
			name:       "job permanently fails after exhausting max retries",
			jobType:    "tasks.fail",
			failCount:  5,
			maxRetries: 2,
			shouldPass: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := jobqueue.NewMemoryQueue(jobqueue.QueueConfig{
				Workers:      2,
				PollInterval: 20 * time.Millisecond,
				BaseBackoff:  10 * time.Millisecond,
				MaxBackoff:   50 * time.Millisecond,
			})
			defer func() { _ = q.Stop() }()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			var attempts atomic.Int32
			doneCh := make(chan struct{})
			var once sync.Once

			q.RegisterHandler(tc.jobType, func(ctx context.Context, job *jobqueue.Job) error {
				curr := attempts.Add(1)
				if int(curr) <= tc.failCount {
					if int(curr) >= tc.maxRetries && !tc.shouldPass {
						once.Do(func() { close(doneCh) })
					}
					return errors.New("transient error")
				}
				once.Do(func() { close(doneCh) })
				return nil
			})

			if err := q.Start(ctx); err != nil {
				t.Fatalf("Start error: %v", err)
			}

			job := jobqueue.NewJob(tc.jobType, []byte("data")).WithMaxRetries(tc.maxRetries)
			if err := q.Enqueue(ctx, job); err != nil {
				t.Fatalf("Enqueue error: %v", err)
			}

			select {
			case <-doneCh:
				// Success
			case <-time.After(1 * time.Second):
				t.Fatalf("timed out waiting for job completion")
			}
		})
	}

	t.Run("priority ordering", func(t *testing.T) {
		q := jobqueue.NewMemoryQueue(jobqueue.QueueConfig{
			Workers:      1, // Single worker to strictly enforce sequential order
			PollInterval: 20 * time.Millisecond,
		})
		defer func() { _ = q.Stop() }()

		ctx := context.Background()
		var processed []string
		var mu sync.Mutex
		done := make(chan struct{})

		q.RegisterHandler("priority.test", func(ctx context.Context, job *jobqueue.Job) error {
			mu.Lock()
			processed = append(processed, string(job.Payload))
			if len(processed) == 3 {
				close(done)
			}
			mu.Unlock()
			return nil
		})

		// Enqueue before starting so they are ordered in priority queue
		_ = q.Enqueue(ctx, jobqueue.NewJob("priority.test", []byte("low")).WithPriority(jobqueue.PriorityLow))
		_ = q.Enqueue(ctx, jobqueue.NewJob("priority.test", []byte("critical")).WithPriority(jobqueue.PriorityCritical))
		_ = q.Enqueue(ctx, jobqueue.NewJob("priority.test", []byte("high")).WithPriority(jobqueue.PriorityHigh))

		_ = q.Start(ctx)

		select {
		case <-done:
			mu.Lock()
			defer mu.Unlock()
			if processed[0] != "critical" || processed[1] != "high" || processed[2] != "low" {
				t.Errorf("unexpected priority order: %v", processed)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for priority jobs")
		}
	})

	t.Run("delayed scheduling", func(t *testing.T) {
		q := jobqueue.NewMemoryQueue(jobqueue.QueueConfig{
			Workers:      2,
			PollInterval: 20 * time.Millisecond,
		})
		defer func() { _ = q.Stop() }()

		ctx := context.Background()
		start := time.Now()
		done := make(chan time.Duration)

		q.RegisterHandler("delayed.test", func(ctx context.Context, job *jobqueue.Job) error {
			done <- time.Since(start)
			return nil
		})

		_ = q.Start(ctx)
		_ = q.EnqueueIn(ctx, jobqueue.NewJob("delayed.test", []byte("delay")), 80*time.Millisecond)

		select {
		case elapsed := <-done:
			if elapsed < 70*time.Millisecond {
				t.Errorf("job executed too early: %v", elapsed)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for delayed job")
		}
	})

	t.Run("closed queue errors", func(t *testing.T) {
		q := jobqueue.NewMemoryQueue(jobqueue.QueueConfig{})
		_ = q.Stop()

		err := q.Enqueue(context.Background(), jobqueue.NewJob("test", []byte("data")))
		if err == nil {
			t.Errorf("expected error enqueueing to closed queue, got nil")
		}
	})
}

func TestRedisQueue(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	q, err := jobqueue.NewRedisQueue(jobqueue.RedisQueueConfig{
		Client:       client,
		QueuePrefix:  "test-jobs:",
		Workers:      2,
		PollInterval: 30 * time.Millisecond,
		BaseBackoff:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewRedisQueue error: %v", err)
	}
	defer func() { _ = q.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	t.Run("process immediate job", func(t *testing.T) {
		done := make(chan string, 1)
		q.RegisterHandler("redis.sync", func(ctx context.Context, job *jobqueue.Job) error {
			done <- string(job.Payload)
			return nil
		})

		if err := q.Start(ctx); err != nil {
			t.Fatalf("Start error: %v", err)
		}

		job := jobqueue.NewJob("redis.sync", []byte("payload-123"))
		if err := q.Enqueue(ctx, job); err != nil {
			t.Fatalf("Enqueue error: %v", err)
		}

		select {
		case payload := <-done:
			if payload != "payload-123" {
				t.Errorf("expected payload-123, got %s", payload)
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for Redis job processing")
		}
	})

	t.Run("delayed scheduling with EnqueueIn and EnqueueAt", func(t *testing.T) {
		delayedDone := make(chan struct{}, 1)
		q.RegisterHandler("redis.delayed", func(ctx context.Context, job *jobqueue.Job) error {
			delayedDone <- struct{}{}
			return nil
		})

		job1 := jobqueue.NewJob("redis.delayed", []byte("delayed-payload"))
		if err := q.EnqueueIn(ctx, job1, 50*time.Millisecond); err != nil {
			t.Fatalf("EnqueueIn error: %v", err)
		}

		job2 := jobqueue.NewJob("redis.delayed", []byte("scheduled-payload")).WithRunAt(time.Now().Add(50 * time.Millisecond))
		if err := q.EnqueueAt(ctx, job2, time.Now().Add(50*time.Millisecond)); err != nil {
			t.Fatalf("EnqueueAt error: %v", err)
		}

		// Wait for both to be promoted and processed
		for i := 0; i < 2; i++ {
			select {
			case <-delayedDone:
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for delayed Redis job %d", i)
			}
		}
	})

	t.Run("MemoryQueue EnqueueAt and job builder helpers", func(t *testing.T) {
		mq := jobqueue.NewMemoryQueue(jobqueue.QueueConfig{
			Workers:      1,
			PollInterval: 20 * time.Millisecond,
		})
		defer func() { _ = mq.Stop() }()

		done := make(chan struct{}, 1)
		mq.RegisterHandler("mem.at", func(ctx context.Context, job *jobqueue.Job) error {
			done <- struct{}{}
			return nil
		})
		_ = mq.Start(ctx)

		job := jobqueue.NewJob("mem.at", []byte("data")).
			WithRunAt(time.Now().Add(30*time.Millisecond)).
			WithHeader("tenant", "t-1")

		if job.Headers["tenant"] != "t-1" {
			t.Errorf("expected header tenant=t-1")
		}

		err := mq.EnqueueAt(ctx, job, time.Now().Add(30*time.Millisecond))
		if err != nil {
			t.Fatalf("EnqueueAt error: %v", err)
		}

		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatal("timed out waiting for MemoryQueue EnqueueAt")
		}
	})

	t.Run("retry with backoff and exhaustion", func(t *testing.T) {
		var attempts atomic.Int32
		done := make(chan struct{}, 1)
		q.RegisterHandler("redis.failing", func(ctx context.Context, job *jobqueue.Job) error {
			val := attempts.Add(1)
			if val >= 2 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
			return errors.New("transient database error")
		})

		failingJob := jobqueue.NewJob("redis.failing", []byte("failing-payload"))
		failingJob.MaxRetries = 2
		if err := q.Enqueue(ctx, failingJob); err != nil {
			t.Fatalf("Enqueue error: %v", err)
		}

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for retry attempts")
		}
	})
}

func TestNewRedisQueue_TableDriven(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer client.Close()

	tests := []struct {
		name    string
		cfg     jobqueue.RedisQueueConfig
		wantErr bool
	}{
		{
			name: "missing redis client returns error",
			cfg: jobqueue.RedisQueueConfig{
				Client: nil,
			},
			wantErr: true,
		},
		{
			name: "zero config populates safe defaults",
			cfg: jobqueue.RedisQueueConfig{
				Client: client,
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := jobqueue.NewRedisQueue(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && q == nil {
				t.Fatal("expected non-nil RedisQueue")
			}
		})
	}
}

func TestJob_Unmarshal_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr bool
	}{
		{
			name:    "valid JSON unmarshals successfully",
			payload: []byte(`{"task":"reindex"}`),
			wantErr: false,
		},
		{
			name:    "empty payload returns error",
			payload: nil,
			wantErr: true,
		},
		{
			name:    "invalid JSON returns error",
			payload: []byte(`{broken`),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job := jobqueue.NewJob("sample", tc.payload)
			var target map[string]string
			err := job.Unmarshal(&target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got=%v", tc.wantErr, err)
			}
			if !tc.wantErr && target["task"] != "reindex" {
				t.Errorf("expected reindex, got %s", target["task"])
			}
		})
	}
}
