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

	t.Run("int and float typed evaluations and SetFlag", func(t *testing.T) {
		client.SetFlag(featureflag.Flag{
			Key:              "rate-limits",
			Enabled:          true,
			DefaultVariation: "int_limit",
			Variations: map[string]any{
				"int_limit":   100,
				"float_limit": 50.5,
				"int64_limit": int64(200),
			},
		})

		// Int evaluation from int
		if got := client.IntValue(ctx, "rate-limits", 0, evalCtx); got != 100 {
			t.Errorf("expected int 100, got %d", got)
		}

		// Update flag to float
		client.SetFlag(featureflag.Flag{
			Key:              "rate-limits",
			Enabled:          true,
			DefaultVariation: "float_limit",
			Variations: map[string]any{
				"int_limit":   100,
				"float_limit": 50.5,
				"int64_limit": int64(200),
			},
		})

		// Int evaluation from float
		if got := client.IntValue(ctx, "rate-limits", 0, evalCtx); got != 50 {
			t.Errorf("expected int 50 from float 50.5, got %d", got)
		}

		// Float evaluation from float
		if got := client.FloatValue(ctx, "rate-limits", 0.0, evalCtx); got != 50.5 {
			t.Errorf("expected float 50.5, got %f", got)
		}

		// Update to int64
		client.SetFlag(featureflag.Flag{
			Key:              "rate-limits",
			Enabled:          true,
			DefaultVariation: "int64_limit",
			Variations: map[string]any{
				"int64_limit": int64(200),
			},
		})
		if got := client.IntValue(ctx, "rate-limits", 0, evalCtx); got != 200 {
			t.Errorf("expected int 200 from int64, got %d", got)
		}

		// Type mismatch fallback cases
		if got := client.BooleanValue(ctx, "rate-limits", true, evalCtx); got != true {
			t.Errorf("expected fallback true on non-bool variation")
		}
		if got := client.StringValue(ctx, "rate-limits", "fallback-str", evalCtx); got != "fallback-str" {
			t.Errorf("expected fallback string on non-string variation")
		}
		if got := client.IntValue(ctx, "rate-limits", 999, featureflag.NewContext("u").WithAttribute("k", "v")); got != 200 {
			t.Errorf("expected 200")
		}

		// NewBoolFlag with true
		bf := featureflag.NewBoolFlag("true-flag", true, true)
		if bf.DefaultVariation != "true" {
			t.Errorf("expected default variation true")
		}

		// Context with nil Attributes initialized in WithAttribute
		emptyCtx := featureflag.Context{}
		updated := emptyCtx.WithAttribute("attr", "val")
		if updated.Attributes["attr"] != "val" {
			t.Errorf("expected attribute set")
		}
	})
}

func TestRuleOperators_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		rule     featureflag.Rule
		ctx      featureflag.Context
		expected bool
	}{
		{
			name: "OpNotEquals matches when attribute does not equal value",
			rule: featureflag.Rule{
				Attribute: "country",
				Operator:  featureflag.OpNotEquals,
				Values:    []string{"CA"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("country", "US"),
			expected: true,
		},
		{
			name: "OpIn matches when attribute is in value list",
			rule: featureflag.Rule{
				Attribute: "tier",
				Operator:  featureflag.OpIn,
				Values:    []string{"gold", "platinum"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("tier", "platinum"),
			expected: true,
		},
		{
			name: "OpIn fails when attribute is not in value list",
			rule: featureflag.Rule{
				Attribute: "tier",
				Operator:  featureflag.OpIn,
				Values:    []string{"gold", "platinum"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("tier", "bronze"),
			expected: false,
		},
		{
			name: "OpNotIn matches when attribute is not in blacklist",
			rule: featureflag.Rule{
				Attribute: "region",
				Operator:  featureflag.OpNotIn,
				Values:    []string{"eu-west", "ap-south"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("region", "us-east"),
			expected: true,
		},
		{
			name: "OpNotIn fails when attribute is in blacklist",
			rule: featureflag.Rule{
				Attribute: "region",
				Operator:  featureflag.OpNotIn,
				Values:    []string{"eu-west", "ap-south"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("region", "eu-west"),
			expected: false,
		},
		{
			name: "OpContains matches substring",
			rule: featureflag.Rule{
				Attribute: "email",
				Operator:  featureflag.OpContains,
				Values:    []string{"@corp.com"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("email", "alice@corp.com"),
			expected: true,
		},
		{
			name: "OpStartsWith matches prefix",
			rule: featureflag.Rule{
				Attribute: "version",
				Operator:  featureflag.OpStartsWith,
				Values:    []string{"v2."},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("version", "v2.1.0"),
			expected: true,
		},
		{
			name: "OpEndsWith matches suffix",
			rule: featureflag.Rule{
				Attribute: "filename",
				Operator:  featureflag.OpEndsWith,
				Values:    []string{".json"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("filename", "config.json"),
			expected: true,
		},
		{
			name: "missing attribute returns false",
			rule: featureflag.Rule{
				Attribute: "non_existent",
				Operator:  featureflag.OpEquals,
				Values:    []string{"val"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1"),
			expected: false,
		},
		{
			name: "unknown operator returns false",
			rule: featureflag.Rule{
				Attribute: "country",
				Operator:  "UNKNOWN_OP",
				Values:    []string{"US"},
				Variation: "active",
			},
			ctx:      featureflag.NewContext("u1").WithAttribute("country", "US"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flag := featureflag.Flag{
				Key:              "test-flag",
				Enabled:          true,
				DefaultVariation: "off",
				Variations: map[string]any{
					"active": true,
					"off":    false,
				},
				Rules: []featureflag.Rule{tc.rule},
			}
			val := featureflag.Evaluate(flag, tc.ctx)
			got, ok := val.(bool)
			if !ok {
				t.Fatalf("expected bool variation, got %T (%v)", val, val)
			}
			if got != tc.expected {
				t.Errorf("got %v, expected %v", got, tc.expected)
			}
		})
	}
}
