package search

import (
	"strings"
)

// Query is the common interface implemented by all search query clauses.
type Query interface {
	// Validate checks that the query has necessary parameters.
	Validate() error
}

// TermQuery matches documents containing an exact term in a specific field.
type TermQuery struct {
	Field string  `json:"field"`
	Term  string  `json:"term"`
	Boost float64 `json:"boost,omitempty"`
}

// NewTermQuery creates a new TermQuery.
func NewTermQuery(field, term string) *TermQuery {
	return &TermQuery{Field: field, Term: term, Boost: 1.0}
}

// WithBoost sets the score multiplier for this query clause.
func (q *TermQuery) WithBoost(boost float64) *TermQuery {
	q.Boost = boost
	return q
}

// Validate checks for non-empty field and term.
func (q *TermQuery) Validate() error {
	if strings.TrimSpace(q.Field) == "" {
		return ErrInvalidField
	}
	if strings.TrimSpace(q.Term) == "" {
		return ErrInvalidQuery
	}
	return nil
}

// MatchQuery analyzes the input text into tokens and searches the specified field using BM25.
type MatchQuery struct {
	Field    string    `json:"field"`
	Text     string    `json:"text"`
	Operator string    `json:"operator,omitempty"` // "OR" (default) or "AND"
	Analyzer *Analyzer `json:"-"`
	Boost    float64   `json:"boost,omitempty"`
}

// NewMatchQuery creates a new MatchQuery.
func NewMatchQuery(field, text string) *MatchQuery {
	return &MatchQuery{
		Field:    field,
		Text:     text,
		Operator: "OR",
		Boost:    1.0,
	}
}

// WithOperator sets boolean coordination between analyzed tokens ("AND" or "OR").
func (q *MatchQuery) WithOperator(op string) *MatchQuery {
	upper := strings.ToUpper(op)
	if upper == "AND" || upper == "OR" {
		q.Operator = upper
	}
	return q
}

// WithAnalyzer sets a custom analyzer for query text parsing.
func (q *MatchQuery) WithAnalyzer(a *Analyzer) *MatchQuery {
	q.Analyzer = a
	return q
}

// WithBoost sets the score multiplier.
func (q *MatchQuery) WithBoost(boost float64) *MatchQuery {
	q.Boost = boost
	return q
}

// Validate checks that field and text are non-empty.
func (q *MatchQuery) Validate() error {
	if strings.TrimSpace(q.Field) == "" {
		return ErrInvalidField
	}
	if strings.TrimSpace(q.Text) == "" {
		return ErrInvalidQuery
	}
	return nil
}

// MultiMatchQuery executes a match query across multiple fields with field-level boosts.
type MultiMatchQuery struct {
	Fields      []string           `json:"fields"`
	FieldBoosts map[string]float64 `json:"field_boosts,omitempty"`
	Text        string             `json:"text"`
	Operator    string             `json:"operator,omitempty"`
	Analyzer    *Analyzer          `json:"-"`
	Boost       float64            `json:"boost,omitempty"`
}

// NewMultiMatchQuery creates a MultiMatchQuery across several fields.
func NewMultiMatchQuery(text string, fields ...string) *MultiMatchQuery {
	return &MultiMatchQuery{
		Fields:      fields,
		FieldBoosts: make(map[string]float64),
		Text:        text,
		Operator:    "OR",
		Boost:       1.0,
	}
}

// SetFieldBoost assigns a specific weight boost to a field (e.g., title: 2.0).
func (q *MultiMatchQuery) SetFieldBoost(field string, boost float64) *MultiMatchQuery {
	if q.FieldBoosts == nil {
		q.FieldBoosts = make(map[string]float64)
	}
	q.FieldBoosts[field] = boost
	return q
}

// WithOperator sets the operator ("OR" or "AND").
func (q *MultiMatchQuery) WithOperator(op string) *MultiMatchQuery {
	upper := strings.ToUpper(op)
	if upper == "AND" || upper == "OR" {
		q.Operator = upper
	}
	return q
}

// WithAnalyzer sets a custom analyzer.
func (q *MultiMatchQuery) WithAnalyzer(a *Analyzer) *MultiMatchQuery {
	q.Analyzer = a
	return q
}

// WithBoost sets the query-level multiplier.
func (q *MultiMatchQuery) WithBoost(boost float64) *MultiMatchQuery {
	q.Boost = boost
	return q
}

// Validate ensures fields and text are non-empty.
func (q *MultiMatchQuery) Validate() error {
	if len(q.Fields) == 0 {
		return ErrInvalidField
	}
	if strings.TrimSpace(q.Text) == "" {
		return ErrInvalidQuery
	}
	return nil
}

// BoolQuery combines multiple query clauses using boolean logic.
type BoolQuery struct {
	Must               []Query `json:"must,omitempty"`
	Should             []Query `json:"should,omitempty"`
	MustNot            []Query `json:"must_not,omitempty"`
	Filter             []Query `json:"filter,omitempty"`
	MinimumShouldMatch int     `json:"minimum_should_match,omitempty"`
	Boost              float64 `json:"boost,omitempty"`
}

// NewBoolQuery creates an empty BoolQuery with neutral boost.
func NewBoolQuery() *BoolQuery {
	return &BoolQuery{
		Must:    make([]Query, 0),
		Should:  make([]Query, 0),
		MustNot: make([]Query, 0),
		Filter:  make([]Query, 0),
		Boost:   1.0,
	}
}

// MustClause adds queries that MUST match (AND condition, adds to score).
func (q *BoolQuery) MustClause(queries ...Query) *BoolQuery {
	q.Must = append(q.Must, queries...)
	return q
}

// ShouldClause adds queries where at least one SHOULD match (OR condition, adds to score).
func (q *BoolQuery) ShouldClause(queries ...Query) *BoolQuery {
	q.Should = append(q.Should, queries...)
	return q
}

// MustNotClause adds queries that MUST NOT match (NOT condition, excludes documents).
func (q *BoolQuery) MustNotClause(queries ...Query) *BoolQuery {
	q.MustNot = append(q.MustNot, queries...)
	return q
}

// FilterClause adds queries that MUST match but do NOT affect scoring (fast binary filter).
func (q *BoolQuery) FilterClause(queries ...Query) *BoolQuery {
	q.Filter = append(q.Filter, queries...)
	return q
}

// SetMinimumShouldMatch sets how many Should clauses must match when Must is not present.
func (q *BoolQuery) SetMinimumShouldMatch(n int) *BoolQuery {
	q.MinimumShouldMatch = n
	return q
}

// WithBoost sets the overall boost for the boolean query.
func (q *BoolQuery) WithBoost(boost float64) *BoolQuery {
	q.Boost = boost
	return q
}

// Validate verifies that at least one clause is provided and each clause is valid.
func (q *BoolQuery) Validate() error {
	if len(q.Must) == 0 && len(q.Should) == 0 && len(q.MustNot) == 0 && len(q.Filter) == 0 {
		return ErrInvalidQuery
	}
	for _, clauses := range [][]Query{q.Must, q.Should, q.MustNot, q.Filter} {
		for _, clause := range clauses {
			if clause == nil {
				return ErrInvalidQuery
			}
			if err := clause.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// PrefixQuery matches documents where terms in the field begin with the given prefix.
type PrefixQuery struct {
	Field  string  `json:"field"`
	Prefix string  `json:"prefix"`
	Boost  float64 `json:"boost,omitempty"`
}

// NewPrefixQuery creates a PrefixQuery.
func NewPrefixQuery(field, prefix string) *PrefixQuery {
	return &PrefixQuery{Field: field, Prefix: prefix, Boost: 1.0}
}

// WithBoost sets the boost multiplier.
func (q *PrefixQuery) WithBoost(boost float64) *PrefixQuery {
	q.Boost = boost
	return q
}

// Validate verifies field and prefix.
func (q *PrefixQuery) Validate() error {
	if strings.TrimSpace(q.Field) == "" {
		return ErrInvalidField
	}
	if strings.TrimSpace(q.Prefix) == "" {
		return ErrInvalidQuery
	}
	return nil
}

// FuzzyQuery matches terms within a specified Levenshtein edit distance.
type FuzzyQuery struct {
	Field        string  `json:"field"`
	Term         string  `json:"term"`
	MaxEdits     int     `json:"max_edits"`     // Max Levenshtein distance (1 or 2, default 2)
	PrefixLength int     `json:"prefix_length"` // Common prefix required (default 0)
	Boost        float64 `json:"boost,omitempty"`
}

// NewFuzzyQuery creates a FuzzyQuery with default max edits of 2.
func NewFuzzyQuery(field, term string) *FuzzyQuery {
	return &FuzzyQuery{
		Field:        field,
		Term:         term,
		MaxEdits:     2,
		PrefixLength: 0,
		Boost:        1.0,
	}
}

// WithMaxEdits sets the maximum Levenshtein edit distance (1 or 2).
func (q *FuzzyQuery) WithMaxEdits(edits int) *FuzzyQuery {
	if edits < 1 {
		edits = 1
	}
	if edits > 2 {
		edits = 2
	}
	q.MaxEdits = edits
	return q
}

// WithPrefixLength sets the number of initial characters that must match exactly.
func (q *FuzzyQuery) WithPrefixLength(len int) *FuzzyQuery {
	if len >= 0 {
		q.PrefixLength = len
	}
	return q
}

// WithBoost sets the score multiplier.
func (q *FuzzyQuery) WithBoost(boost float64) *FuzzyQuery {
	q.Boost = boost
	return q
}

// Validate checks field and term.
func (q *FuzzyQuery) Validate() error {
	if strings.TrimSpace(q.Field) == "" {
		return ErrInvalidField
	}
	if strings.TrimSpace(q.Term) == "" {
		return ErrInvalidQuery
	}
	return nil
}

// PhraseQuery matches documents containing words in exact adjacent positions or with a maximum slop.
type PhraseQuery struct {
	Field string   `json:"field"`
	Terms []string `json:"terms"`
	Slop  int      `json:"slop"` // Allowed displacement between terms (default 0)
	Boost float64  `json:"boost,omitempty"`
}

// NewPhraseQuery creates a PhraseQuery for exact consecutive words.
func NewPhraseQuery(field string, terms ...string) *PhraseQuery {
	return &PhraseQuery{
		Field: field,
		Terms: terms,
		Slop:  0,
		Boost: 1.0,
	}
}

// WithSlop sets the allowed positional displacement between terms.
func (q *PhraseQuery) WithSlop(slop int) *PhraseQuery {
	if slop >= 0 {
		q.Slop = slop
	}
	return q
}

// WithBoost sets the boost multiplier.
func (q *PhraseQuery) WithBoost(boost float64) *PhraseQuery {
	q.Boost = boost
	return q
}

// Validate checks that field and at least 2 terms are provided.
func (q *PhraseQuery) Validate() error {
	if strings.TrimSpace(q.Field) == "" {
		return ErrInvalidField
	}
	if len(q.Terms) < 2 {
		return ErrInvalidQuery
	}
	return nil
}

// RangeQuery performs filtering on numeric fields within [Min, Max] bounds.
type RangeQuery struct {
	Field string   `json:"field"`
	GTE   *float64 `json:"gte,omitempty"`
	GT    *float64 `json:"gt,omitempty"`
	LTE   *float64 `json:"lte,omitempty"`
	LT    *float64 `json:"lt,omitempty"`
	Boost float64  `json:"boost,omitempty"`
}

// NewRangeQuery creates a RangeQuery for a numeric field.
func NewRangeQuery(field string) *RangeQuery {
	return &RangeQuery{
		Field: field,
		Boost: 1.0,
	}
}

// GreaterThanOrEqual sets inclusive minimum bound.
func (q *RangeQuery) GreaterThanOrEqual(val float64) *RangeQuery {
	q.GTE = &val
	return q
}

// GreaterThan sets exclusive minimum bound.
func (q *RangeQuery) GreaterThan(val float64) *RangeQuery {
	q.GT = &val
	return q
}

// LessThanOrEqual sets inclusive maximum bound.
func (q *RangeQuery) LessThanOrEqual(val float64) *RangeQuery {
	q.LTE = &val
	return q
}

// LessThan sets exclusive maximum bound.
func (q *RangeQuery) LessThan(val float64) *RangeQuery {
	q.LT = &val
	return q
}

// WithBoost sets the boost.
func (q *RangeQuery) WithBoost(boost float64) *RangeQuery {
	q.Boost = boost
	return q
}

// Validate checks that field is set and at least one boundary is present.
func (q *RangeQuery) Validate() error {
	if strings.TrimSpace(q.Field) == "" {
		return ErrInvalidField
	}
	if q.GTE == nil && q.GT == nil && q.LTE == nil && q.LT == nil {
		return ErrInvalidQuery
	}
	return nil
}

// TagQuery performs exact match filtering against Document.Tags.
type TagQuery struct {
	Tag   string  `json:"tag"`
	Boost float64 `json:"boost,omitempty"`
}

// NewTagQuery creates a TagQuery.
func NewTagQuery(tag string) *TagQuery {
	return &TagQuery{Tag: tag, Boost: 1.0}
}

// WithBoost sets the boost.
func (q *TagQuery) WithBoost(boost float64) *TagQuery {
	q.Boost = boost
	return q
}

// Validate ensures tag is non-empty.
func (q *TagQuery) Validate() error {
	if strings.TrimSpace(q.Tag) == "" {
		return ErrInvalidQuery
	}
	return nil
}
