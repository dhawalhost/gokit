package featureflag_test

import (
	"context"
	"testing"

	"github.com/dhawalhost/gokit/featureflag"
)

func TestFeatureFlag_Evaluation(t *testing.T) {
	tests := []struct {
		name       string
		flag       featureflag.Flag
		ctx        featureflag.Context
		defaultVal bool
		want       bool
	}{
		{
			name:       "disabled flag returns default variation",
			flag:       featureflag.NewBoolFlag("beta-ui", false, false),
			ctx:        featureflag.NewContext("user-1"),
			defaultVal: false,
			want:       false,
		},
		{
			name: "enabled flag with matching rule returns variation",
			flag: featureflag.Flag{
				Key:              "dark-mode",
				Enabled:          true,
				DefaultVariation: "false",
				Variations:       map[string]any{"true": true, "false": false},
				Rules: []featureflag.Rule{
					{
						Attribute: "role",
						Operator:  featureflag.OpEquals,
						Values:    []string{"admin"},
						Variation: "true",
					},
				},
			},
			ctx:        featureflag.NewContext("user-10").WithAttribute("role", "admin"),
			defaultVal: false,
			want:       true,
		},
		{
			name: "enabled flag with non-matching rule returns default variation",
			flag: featureflag.Flag{
				Key:              "dark-mode",
				Enabled:          true,
				DefaultVariation: "false",
				Variations:       map[string]any{"true": true, "false": false},
				Rules: []featureflag.Rule{
					{
						Attribute: "role",
						Operator:  featureflag.OpEquals,
						Values:    []string{"admin"},
						Variation: "true",
					},
				},
			},
			ctx:        featureflag.NewContext("user-10").WithAttribute("role", "viewer"),
			defaultVal: false,
			want:       false,
		},
		{
			name: "tenant in rule matches",
			flag: featureflag.Flag{
				Key:              "ai-insights",
				Enabled:          true,
				DefaultVariation: "false",
				Variations:       map[string]any{"true": true, "false": false},
				Rules: []featureflag.Rule{
					{
						Attribute: "tenant_id",
						Operator:  featureflag.OpIn,
						Values:    []string{"acme", "globex"},
						Variation: "true",
					},
				},
			},
			ctx:        featureflag.NewContext("user-5").WithAttribute("tenant_id", "acme"),
			defaultVal: false,
			want:       true,
		},
		{
			name: "email ends_with rule matches",
			flag: featureflag.Flag{
				Key:              "internal-tools",
				Enabled:          true,
				DefaultVariation: "false",
				Variations:       map[string]any{"true": true, "false": false},
				Rules: []featureflag.Rule{
					{
						Attribute: "email",
						Operator:  featureflag.OpEndsWith,
						Values:    []string{"@company.internal"},
						Variation: "true",
					},
				},
			},
			ctx:        featureflag.NewContext("user-7").WithAttribute("email", "dev@company.internal"),
			defaultVal: false,
			want:       true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := featureflag.NewClient(tc.flag)
			got := client.BooleanValue(context.Background(), tc.flag.Key, tc.defaultVal, tc.ctx)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFeatureFlag_RolloutConsistency(t *testing.T) {
	flag := featureflag.Flag{
		Key:              "new-checkout",
		Enabled:          true,
		DefaultVariation: "false",
		Variations:       map[string]any{"true": true, "false": false},
		Rollout: &featureflag.RolloutStrategy{
			Percentage: 50.0, // 50% rollout
		},
	}

	client := featureflag.NewClient(flag)
	ctx := context.Background()

	// Verify consistent hashing: evaluating 100 times for the same user yields identical results
	evalCtx := featureflag.NewContext("user-consistent-42")
	first := client.BooleanValue(ctx, "new-checkout", false, evalCtx)

	for i := 0; i < 100; i++ {
		repeat := client.BooleanValue(ctx, "new-checkout", false, evalCtx)
		if repeat != first {
			t.Fatalf("inconsistent rollout assignment on iteration %d: %v vs %v", i, repeat, first)
		}
	}
}

func TestFeatureFlag_TypedValues(t *testing.T) {
	flag := featureflag.Flag{
		Key:              "pricing-config",
		Enabled:          true,
		DefaultVariation: "default",
		Variations: map[string]any{
			"default": "standard-tier",
			"tier":    "premium-tier",
			"limit":   100,
			"ratio":   0.75,
		},
	}

	client := featureflag.NewClient(flag)
	ctx := context.Background()
	evalCtx := featureflag.NewContext("user-1")

	t.Run("string value", func(t *testing.T) {
		got := client.StringValue(ctx, "pricing-config", "fallback", evalCtx)
		if got != "standard-tier" {
			t.Errorf("expected standard-tier, got %s", got)
		}
	})

	t.Run("int value fallback", func(t *testing.T) {
		got := client.IntValue(ctx, "unknown-flag", 42, evalCtx)
		if got != 42 {
			t.Errorf("expected fallback 42, got %d", got)
		}
	})

	t.Run("float value fallback", func(t *testing.T) {
		got := client.FloatValue(ctx, "unknown-flag", 3.14, evalCtx)
		if got != 3.14 {
			t.Errorf("expected fallback 3.14, got %f", got)
		}
	})
}
