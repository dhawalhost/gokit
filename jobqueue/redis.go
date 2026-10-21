package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisQueueConfig configures a Redis-backed distributed job queue.
type RedisQueueConfig struct {
	Client       redis.UniversalClient
	QueuePrefix  string        // Key prefix (default: "jobqueue:")
	Workers      int           // Number of concurrent worker goroutines (default: 4)
	PollInterval time.Duration // Scheduler interval to promote delayed jobs (default: 1s)
	BaseBackoff  time.Duration // Exponential retry base backoff (default: 1s)
	MaxBackoff   time.Duration // Maximum retry backoff (default: 1m)
}

// RedisQueue provides a distributed background job queue backed by Redis.
type RedisQueue struct {
	cfg      RedisQueueConfig
	client   redis.UniversalClient
	handlers map[string]JobHandler
	hMu      sync.RWMutex
	closeCh  chan struct{}
	wg       sync.WaitGroup
	started  atomic.Bool
	closed   atomic.Bool
}

// NewRedisQueue constructs a new Redis-backed JobQueue.
func NewRedisQueue(cfg RedisQueueConfig) (*RedisQueue, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("jobqueue: redis client is required")
	}
	if cfg.QueuePrefix == "" {
		cfg.QueuePrefix = "jobqueue:"
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 1 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 1 * time.Minute
	}

	return &RedisQueue{
		cfg:      cfg,
		client:   cfg.Client,
		handlers: make(map[string]JobHandler),
		closeCh:  make(chan struct{}),
	}, nil
}

func (q *RedisQueue) readyKey() string {
	return q.cfg.QueuePrefix + "ready"
}

func (q *RedisQueue) delayedKey() string {
	return q.cfg.QueuePrefix + "delayed"
}

// RegisterHandler registers a worker handler for a job type.
func (q *RedisQueue) RegisterHandler(jobType string, handler JobHandler) {
	q.hMu.Lock()
	defer q.hMu.Unlock()
	q.handlers[jobType] = handler
}

// Enqueue puts a job into the ready queue immediately or delayed queue if RunAt is in the future.
func (q *RedisQueue) Enqueue(ctx context.Context, job *Job) error {
	if q.closed.Load() {
		return fmt.Errorf("jobqueue: queue is closed")
	}
	if job == nil {
		return fmt.Errorf("jobqueue: nil job")
	}

	bytes, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("jobqueue: marshal job: %w", err)
	}

	now := time.Now().UTC()
	if !job.RunAt.IsZero() && job.RunAt.After(now) {
		// Put into delayed ZSET with score = UnixNano
		score := float64(job.RunAt.UnixNano())
		return q.client.ZAdd(ctx, q.delayedKey(), redis.Z{Score: score, Member: string(bytes)}).Err()
	}

	// Put into ready queue (List)
	return q.client.LPush(ctx, q.readyKey(), string(bytes)).Err()
}

// EnqueueIn submits a job to run after the given delay.
func (q *RedisQueue) EnqueueIn(ctx context.Context, job *Job, delay time.Duration) error {
	job.WithDelay(delay)
	return q.Enqueue(ctx, job)
}

// EnqueueAt submits a job to run at a specific timestamp.
func (q *RedisQueue) EnqueueAt(ctx context.Context, job *Job, runAt time.Time) error {
	job.WithRunAt(runAt)
	return q.Enqueue(ctx, job)
}

// Start launches worker goroutines and the delayed-job promotion scheduler.
func (q *RedisQueue) Start(ctx context.Context) error {
	if !q.started.CompareAndSwap(false, true) {
		return fmt.Errorf("jobqueue: already started")
	}

	// Delayed job promoter
	q.wg.Add(1)
	go q.scheduler(ctx)

	// Worker pool
	for i := 0; i < q.cfg.Workers; i++ {
		q.wg.Add(1)
		go q.worker(ctx)
	}

	return nil
}

// promoteLua atomically moves mature delayed jobs to the ready list.
var promoteLua = redis.NewScript(`
local delayedKey = KEYS[1]
local readyKey = KEYS[2]
local maxScore = ARGV[1]
local batchSize = ARGV[2]

local jobs = redis.call('zrangebyscore', delayedKey, 0, maxScore, 'LIMIT', 0, batchSize)
if #jobs > 0 then
    for _, job in ipairs(jobs) do
        redis.call('lpush', readyKey, job)
        redis.call('zrem', delayedKey, job)
    end
end
return #jobs
`)

func (q *RedisQueue) scheduler(ctx context.Context) {
	defer q.wg.Done()
	ticker := time.NewTicker(q.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-q.closeCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			nowScore := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
			_ = promoteLua.Run(ctx, q.client, []string{q.delayedKey(), q.readyKey()}, nowScore, 50).Err()
		}
	}
}

func (q *RedisQueue) worker(ctx context.Context) {
	defer q.wg.Done()

	for {
		select {
		case <-q.closeCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		res, err := q.client.BRPop(ctx, 1*time.Second, q.readyKey()).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
				continue
			}
			select {
			case <-q.closeCh:
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		if len(res) < 2 {
			continue
		}

		rawJSON := res[1]
		var job Job
		if err := json.Unmarshal([]byte(rawJSON), &job); err != nil {
			continue
		}

		q.processJob(ctx, &job)
	}
}

func (q *RedisQueue) processJob(ctx context.Context, job *Job) {
	q.hMu.RLock()
	handler, exists := q.handlers[job.Type]
	q.hMu.RUnlock()

	if !exists {
		return
	}

	err := handler(ctx, job)
	if err == nil {
		return
	}

	job.RetryCount++
	job.LastError = err.Error()

	if job.RetryCount >= job.MaxRetries {
		return // Terminate / Dead letter
	}

	// Calculate exponential backoff and re-enqueue to delayed ZSET
	backoff := q.cfg.BaseBackoff * time.Duration(1<<uint(job.RetryCount-1))
	if backoff > q.cfg.MaxBackoff {
		backoff = q.cfg.MaxBackoff
	}

	job.RunAt = time.Now().UTC().Add(backoff)
	bytes, mErr := json.Marshal(job)
	if mErr == nil {
		score := float64(job.RunAt.UnixNano())
		_ = q.client.ZAdd(ctx, q.delayedKey(), redis.Z{Score: score, Member: string(bytes)}).Err()
	}
}

// Stop gracefully signals workers and scheduler to stop.
func (q *RedisQueue) Stop() error {
	if q.closed.CompareAndSwap(false, true) {
		close(q.closeCh)
		q.wg.Wait()
	}
	return nil
}
