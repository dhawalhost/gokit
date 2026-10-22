package jobqueue

import (
	"container/heap"
	"testing"
	"time"
)

func TestJobPriorityQueue_TableDriven(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "Less compares by Priority then by CreatedAt",
			run: func(t *testing.T) {
				jHigh := &Job{ID: "1", Priority: PriorityHigh, CreatedAt: now}
				jLow := &Job{ID: "2", Priority: PriorityLow, CreatedAt: now.Add(-time.Hour)}
				jSameEarlier := &Job{ID: "3", Priority: PriorityNormal, CreatedAt: now.Add(-time.Minute)}
				jSameLater := &Job{ID: "4", Priority: PriorityNormal, CreatedAt: now}

				pq := jobPriorityQueue{jHigh, jLow, jSameEarlier, jSameLater}

				// Priority takes precedence
				if !pq.Less(0, 1) {
					t.Error("expected jHigh to be less than jLow (higher priority pops first)")
				}
				if pq.Less(1, 0) {
					t.Error("expected jLow not to be less than jHigh")
				}

				// CreatedAt breaks ties when priority matches
				if !pq.Less(2, 3) {
					t.Error("expected earlier job to be less than later job when priority matches")
				}
				if pq.Less(3, 2) {
					t.Error("expected later job not to be less than earlier job")
				}
			},
		},
		{
			name: "Heap operations Len, Push, Pop, Swap work correctly",
			run: func(t *testing.T) {
				pq := &jobPriorityQueue{}
				heap.Init(pq)

				j1 := &Job{ID: "1", Priority: PriorityLow, CreatedAt: now}
				j2 := &Job{ID: "2", Priority: PriorityCritical, CreatedAt: now}
				j3 := &Job{ID: "3", Priority: PriorityHigh, CreatedAt: now}

				heap.Push(pq, j1)
				heap.Push(pq, j2)
				heap.Push(pq, j3)

				if pq.Len() != 3 {
					t.Fatalf("expected Len 3, got %d", pq.Len())
				}

				// Swap
				pq.Swap(0, 1)
				// Re-heapify
				heap.Init(pq)

				popped1 := heap.Pop(pq).(*Job)
				if popped1.ID != "2" { // Critical priority
					t.Errorf("expected popped job 2 (critical), got %s", popped1.ID)
				}

				popped2 := heap.Pop(pq).(*Job)
				if popped2.ID != "3" { // High priority
					t.Errorf("expected popped job 3 (high), got %s", popped2.ID)
				}

				popped3 := heap.Pop(pq).(*Job)
				if popped3.ID != "1" { // Low priority
					t.Errorf("expected popped job 1 (low), got %s", popped3.ID)
				}

				if pq.Len() != 0 {
					t.Errorf("expected empty pq, got len %d", pq.Len())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}
