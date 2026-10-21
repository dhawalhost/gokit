package featureflag

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// Evaluate resolves a flag against the evaluation Context and returns the winning value.
func Evaluate(flag Flag, ctx Context) any {
	if !flag.Enabled {
		return flag.Variations[flag.DefaultVariation]
	}

	// 1. Evaluate explicit targeting rules first
	for _, rule := range flag.Rules {
		if matchRule(rule, ctx) {
			if val, ok := flag.Variations[rule.Variation]; ok {
				return val
			}
		}
	}

	// 2. Evaluate percentage-based rollout if configured
	if flag.Rollout != nil && flag.Rollout.Percentage > 0 {
		bucketKey := ctx.TargetingKey
		if flag.Rollout.BucketBy != "" {
			if val, ok := ctx.Attributes[flag.Rollout.BucketBy]; ok {
				bucketKey = fmt.Sprintf("%v", val)
			}
		}

		if bucketKey != "" {
			score := calculateHashRatio(flag.Key, bucketKey)
			if score <= flag.Rollout.Percentage {
				// User is within rollout window
				return flag.Variations["true"]
			}
			return flag.Variations["false"]
		}
	}

	// 3. Fallback to default variation
	return flag.Variations[flag.DefaultVariation]
}

// calculateHashRatio returns a stable float between 0.00 and 100.00 using FNV-1a.
func calculateHashRatio(flagKey, entityID string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(flagKey + ":" + entityID))
	hashValue := h.Sum32()
	return float64(hashValue%10000) / 100.0
}

func matchRule(rule Rule, ctx Context) bool {
	var targetVal string
	if rule.Attribute == "targeting_key" {
		targetVal = ctx.TargetingKey
	} else if val, ok := ctx.Attributes[rule.Attribute]; ok {
		targetVal = fmt.Sprintf("%v", val)
	} else {
		return false
	}

	switch rule.Operator {
	case OpEquals:
		return len(rule.Values) > 0 && targetVal == rule.Values[0]
	case OpNotEquals:
		return len(rule.Values) > 0 && targetVal != rule.Values[0]
	case OpIn:
		for _, v := range rule.Values {
			if targetVal == v {
				return true
			}
		}
		return false
	case OpNotIn:
		for _, v := range rule.Values {
			if targetVal == v {
				return false
			}
		}
		return true
	case OpContains:
		return len(rule.Values) > 0 && strings.Contains(targetVal, rule.Values[0])
	case OpStartsWith:
		return len(rule.Values) > 0 && strings.HasPrefix(targetVal, rule.Values[0])
	case OpEndsWith:
		return len(rule.Values) > 0 && strings.HasSuffix(targetVal, rule.Values[0])
	default:
		return false
	}
}
