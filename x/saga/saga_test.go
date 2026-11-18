package saga_test

import (
	"context"
	"errors"
	"testing"

	"github.com/dhawalhost/gokit/x/saga"
)

func TestSaga_Execution(t *testing.T) {
	tests := []struct {
		name           string
		setup          func() (*saga.Saga, *[]string)
		wantState      saga.State
		wantOrder      []string
		wantErr        bool
		wantFailedStep string
	}{
		{
			name: "all steps succeed",
			setup: func() (*saga.Saga, *[]string) {
				order := &[]string{}
				s := saga.New("order-fulfillment")
				s.AddStep(saga.NewStep("reserve-inventory",
					func(ctx context.Context) error {
						*order = append(*order, "exec:inventory")
						return nil
					},
					func(ctx context.Context) error {
						*order = append(*order, "comp:inventory")
						return nil
					},
				))
				s.AddStep(saga.NewStep("charge-payment",
					func(ctx context.Context) error {
						*order = append(*order, "exec:payment")
						return nil
					},
					func(ctx context.Context) error {
						*order = append(*order, "comp:payment")
						return nil
					},
				))
				return s, order
			},
			wantState: saga.StateCompleted,
			wantOrder: []string{"exec:inventory", "exec:payment"},
			wantErr:   false,
		},
		{
			name: "second step fails triggers reverse compensation",
			setup: func() (*saga.Saga, *[]string) {
				order := &[]string{}
				s := saga.New("order-fulfillment")
				s.AddStep(saga.NewStep("reserve-inventory",
					func(ctx context.Context) error {
						*order = append(*order, "exec:inventory")
						return nil
					},
					func(ctx context.Context) error {
						*order = append(*order, "comp:inventory")
						return nil
					},
				))
				s.AddStep(saga.NewStep("charge-payment",
					func(ctx context.Context) error {
						*order = append(*order, "exec:payment")
						return errors.New("insufficient funds")
					},
					func(ctx context.Context) error {
						*order = append(*order, "comp:payment")
						return nil
					},
				))
				return s, order
			},
			wantState:      saga.StateCompensated,
			wantOrder:      []string{"exec:inventory", "exec:payment", "comp:inventory"},
			wantErr:        true,
			wantFailedStep: "charge-payment",
		},
		{
			name: "compensation error flags state failed",
			setup: func() (*saga.Saga, *[]string) {
				order := &[]string{}
				s := saga.New("order-fulfillment")
				s.AddStep(saga.NewStep("step-1",
					func(ctx context.Context) error {
						return nil
					},
					func(ctx context.Context) error {
						return errors.New("cannot compensate step 1")
					},
				))
				s.AddStep(saga.NewStep("step-2",
					func(ctx context.Context) error {
						return errors.New("step 2 failed")
					},
					nil,
				))
				return s, order
			},
			wantState:      saga.StateFailed,
			wantOrder:      []string{},
			wantErr:        true,
			wantFailedStep: "step-2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, order := tc.setup()
			res, err := s.Execute(context.Background())

			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if res.State != tc.wantState {
				t.Errorf("expected state %s, got %s", tc.wantState, res.State)
			}

			if tc.wantFailedStep != "" && res.FailedStep != tc.wantFailedStep {
				t.Errorf("expected failed step %s, got %s", tc.wantFailedStep, res.FailedStep)
			}

			if len(tc.wantOrder) > 0 {
				if len(*order) != len(tc.wantOrder) {
					t.Fatalf("order mismatch length: got %v, want %v", *order, tc.wantOrder)
				}
				for i := range tc.wantOrder {
					if (*order)[i] != tc.wantOrder[i] {
						t.Errorf("step %d order mismatch: got %s, want %s", i, (*order)[i], tc.wantOrder[i])
					}
				}
			}
		})
	}
}

type recordingSagaStore struct {
	snapshots []*saga.ExecutionResult
}

func (r *recordingSagaStore) SaveState(_ context.Context, res *saga.ExecutionResult) error {
	r.snapshots = append(r.snapshots, res)
	return nil
}

func TestSaga_WithStoreCheckpoints(t *testing.T) {
	t.Run("persists deep-copied step and completion checkpoints on success", func(t *testing.T) {
		store := &recordingSagaStore{}
		s := saga.New("checkout").WithID("ord-42").WithStore(store).
			AddStep(saga.NewStep("reserve", func(context.Context) error { return nil }, nil)).
			AddStep(saga.NewStep("capture", func(context.Context) error { return nil }, nil))

		res, err := s.Execute(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.SagaID != "ord-42" || res.State != saga.StateCompleted {
			t.Fatalf("unexpected result: %+v", res)
		}
		// 2 step checkpoints + 1 terminal completed checkpoint = 3.
		if len(store.snapshots) != 3 {
			t.Fatalf("expected 3 checkpoints, got %d", len(store.snapshots))
		}
		if store.snapshots[0].State != saga.StateRunning || len(store.snapshots[0].StepResults) != 1 {
			t.Fatalf("expected first checkpoint running with 1 step, got %+v", store.snapshots[0])
		}
		if store.snapshots[2].State != saga.StateCompleted || len(store.snapshots[2].StepResults) != 2 {
			t.Fatalf("expected final checkpoint completed with 2 steps, got %+v", store.snapshots[2])
		}
	})

	t.Run("persists compensating and compensated checkpoints on failure", func(t *testing.T) {
		store := &recordingSagaStore{}
		s := saga.New("checkout").WithID("ord-99").WithStore(store).
			AddStep(saga.NewStep("reserve",
				func(context.Context) error { return nil },
				func(context.Context) error { return nil },
			)).
			AddStep(saga.NewStep("capture",
				func(context.Context) error { return errors.New("declined") },
				nil,
			))

		res, err := s.Execute(context.Background())
		if err == nil || res.State != saga.StateCompensated {
			t.Fatalf("expected compensated failure, got state=%s err=%v", res.State, err)
		}
		last := store.snapshots[len(store.snapshots)-1]
		if last.State != saga.StateCompensated || last.FailedStep != "capture" {
			t.Fatalf("expected terminal compensated checkpoint for 'capture', got %+v", last)
		}
		if len(last.StepResults) != 2 || last.StepResults[0].Status != saga.StepStatusCompensated {
			t.Fatalf("expected step 0 marked compensated in final checkpoint, got %+v", last.StepResults)
		}
	})
}
