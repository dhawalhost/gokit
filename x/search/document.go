package search

import "time"

// Document represents a searchable record in the index.
type Document struct {
	// ID is the unique primary key of the document.
	ID string `json:"id"`

	// Fields contains string text fields indexed for full-text search.
	Fields map[string]string `json:"fields,omitempty"`

	// NumericFields contains floating-point values for numeric range filtering and sorting.
	NumericFields map[string]float64 `json:"numeric_fields,omitempty"`

	// Tags contains exact-match categorical tokens (e.g., status, tenant, category).
	Tags []string `json:"tags,omitempty"`
}

// NewDocument creates a new Document initialized with empty maps and slices.
func NewDocument(id string) *Document {
	return &Document{
		ID:            id,
		Fields:        make(map[string]string),
		NumericFields: make(map[string]float64),
		Tags:          make([]string, 0),
	}
}

// SetField sets a string text field on the document.
func (d *Document) SetField(name, value string) *Document {
	if d.Fields == nil {
		d.Fields = make(map[string]string)
	}
	d.Fields[name] = value
	return d
}

// GetField retrieves a string field value, returning an empty string if not present.
func (d *Document) GetField(name string) string {
	if d.Fields == nil {
		return ""
	}
	return d.Fields[name]
}

// SetNumeric sets a numeric field value for range filtering.
func (d *Document) SetNumeric(name string, value float64) *Document {
	if d.NumericFields == nil {
		d.NumericFields = make(map[string]float64)
	}
	d.NumericFields[name] = value
	return d
}

// GetNumeric retrieves a numeric field value and whether it was found.
func (d *Document) GetNumeric(name string) (float64, bool) {
	if d.NumericFields == nil {
		return 0, false
	}
	v, ok := d.NumericFields[name]
	return v, ok
}

// AddTag appends one or more exact tags to the document.
func (d *Document) AddTag(tags ...string) *Document {
	d.Tags = append(d.Tags, tags...)
	return d
}

// HasTag checks if the document contains a specific tag (exact match).
func (d *Document) HasTag(tag string) bool {
	for _, t := range d.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Hit represents a single matched document in a search result with its relevance score.
type Hit struct {
	// DocID is the ID of the matched document.
	DocID string `json:"doc_id"`

	// Score is the computed BM25 relevance score.
	Score float64 `json:"score"`

	// Document is the matched Document pointer.
	Document *Document `json:"document,omitempty"`

	// Highlights holds contextual snippets with matched terms highlighted in `<em>...</em>`.
	Highlights map[string][]string `json:"highlights,omitempty"`
}

// Result holds the collection of matched hits and query performance metadata.
type Result struct {
	// TotalHits is the total number of documents matching the query before pagination.
	TotalHits int `json:"total_hits"`

	// Hits contains the ordered slice of hits for the requested page.
	Hits []*Hit `json:"hits"`

	// Took is the elapsed time spent executing the search query.
	Took time.Duration `json:"took"`
}

// Request parameters for querying the index.
type Request struct {
	// Query is the query clause to evaluate against the index.
	Query Query

	// Limit is the maximum number of results to return (default: 10).
	Limit int

	// Offset is the number of results to skip for pagination (default: 0).
	Offset int

	// HighlightFields specifies which fields should have highlight snippets generated.
	HighlightFields []string

	// FilterFunc is an optional predicate function applied after query evaluation to filter documents.
	FilterFunc func(doc *Document) bool
}
