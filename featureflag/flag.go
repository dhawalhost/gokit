package featureflag

// Operator defines conditional operations for targeting rules.
type Operator string

// Supported operators for targeting rules.
const (
	OpEquals      Operator = "equals"
	OpNotEquals   Operator = "not_equals"
	OpIn          Operator = "in"
	OpNotIn       Operator = "not_in"
	OpContains    Operator = "contains"
	OpStartsWith  Operator = "starts_with"
	OpEndsWith    Operator = "ends_with"
	OpGreaterThan Operator = "greater_than"
	OpLessThan    Operator = "less_than"
)

// Rule defines attribute-based matching criteria for targeting variations.
type Rule struct {
	Attribute string   `json:"attribute"`
	Operator  Operator `json:"operator"`
	Values    []string `json:"values"`
	Variation string   `json:"variation"`
}

// RolloutStrategy defines percentage-based rollout parameters.
type RolloutStrategy struct {
	Percentage float64            `json:"percentage"` // 0.0 to 100.0
	BucketBy   string             `json:"bucket_by"`  // e.g. "targeting_key", "tenant_id"
	Variations map[string]float64 `json:"variations"` // variation -> percentage
}

// Flag defines a feature flag configuration.
type Flag struct {
	Key              string           `json:"key"`
	Enabled          bool             `json:"enabled"`
	DefaultVariation string           `json:"default_variation"`
	Variations       map[string]any   `json:"variations"`
	Rules            []Rule           `json:"rules,omitempty"`
	Rollout          *RolloutStrategy `json:"rollout,omitempty"`
}

// Context contains attributes evaluated during flag resolution.
type Context struct {
	TargetingKey string         `json:"targeting_key"`
	Attributes   map[string]any `json:"attributes"`
}

// NewContext creates a new evaluation Context.
func NewContext(targetingKey string) Context {
	return Context{
		TargetingKey: targetingKey,
		Attributes:   make(map[string]any),
	}
}

// WithAttribute sets a metadata attribute on the context.
func (c Context) WithAttribute(key string, value any) Context {
	if c.Attributes == nil {
		c.Attributes = make(map[string]any)
	}
	c.Attributes[key] = value
	return c
}
