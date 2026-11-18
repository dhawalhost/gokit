package search

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Index defines the public API for the search index.
type Index interface {
	// Index adds or updates a document in the index.
	Index(ctx context.Context, doc *Document) error

	// IndexBatch atomically indexes multiple documents.
	IndexBatch(ctx context.Context, docs []*Document) error

	// Delete removes a document by its ID.
	Delete(ctx context.Context, id string) error

	// Get retrieves a document by its ID.
	Get(ctx context.Context, id string) (*Document, error)

	// Search evaluates a search request and returns matching hits ranked by relevance.
	Search(ctx context.Context, req Request) (*Result, error)

	// DocCount returns the current number of documents in the index.
	DocCount() int

	// Close releases index resources and marks the index as closed.
	Close() error
}

// IndexConfig holds tuning parameters for BM25 ranking and text analysis.
type IndexConfig struct {
	// DefaultAnalyzer is used when no field-specific analyzer is configured.
	DefaultAnalyzer *Analyzer

	// FieldAnalyzers maps specific field names to custom analyzers.
	FieldAnalyzers map[string]*Analyzer

	// K1 controls BM25 term frequency saturation (typical range [1.2, 2.0], default 1.2).
	K1 float64

	// B controls BM25 document length normalization (typical range [0.5, 0.8], default 0.75).
	B float64
}

// Posting records occurrences of a term within a specific document.
type Posting struct {
	DocID     string
	TermFreq  int
	Positions []int
}

// MemoryIndex is a thread-safe, high-performance in-memory full-text search engine.
type MemoryIndex struct {
	cfg IndexConfig
	mu  sync.RWMutex

	// docID -> *Document
	docs map[string]*Document

	// field -> term -> docID -> *Posting
	postings map[string]map[string]map[string]*Posting

	// docID -> field -> term count
	docFieldLens map[string]map[string]int

	// field -> total term count across all docs
	totalFieldLens map[string]int64

	// tag -> set of docIDs
	tagIndex map[string]map[string]struct{}

	closed bool
}

// NewMemoryIndex constructs an initialized MemoryIndex.
func NewMemoryIndex(cfg ...IndexConfig) *MemoryIndex {
	c := IndexConfig{
		DefaultAnalyzer: NewStandardAnalyzer(),
		FieldAnalyzers:  make(map[string]*Analyzer),
		K1:              1.2,
		B:               0.75,
	}
	if len(cfg) > 0 {
		userCfg := cfg[0]
		if userCfg.DefaultAnalyzer != nil {
			c.DefaultAnalyzer = userCfg.DefaultAnalyzer
		}
		if userCfg.FieldAnalyzers != nil {
			c.FieldAnalyzers = userCfg.FieldAnalyzers
		}
		if userCfg.K1 > 0 {
			c.K1 = userCfg.K1
		}
		if userCfg.B > 0 {
			c.B = userCfg.B
		}
	}

	return &MemoryIndex{
		cfg:            c,
		docs:           make(map[string]*Document),
		postings:       make(map[string]map[string]map[string]*Posting),
		docFieldLens:   make(map[string]map[string]int),
		totalFieldLens: make(map[string]int64),
		tagIndex:       make(map[string]map[string]struct{}),
	}
}

func (idx *MemoryIndex) analyzerForField(field string) *Analyzer {
	if a, ok := idx.cfg.FieldAnalyzers[field]; ok && a != nil {
		return a
	}
	return idx.cfg.DefaultAnalyzer
}

// Index adds or updates a document in the index.
func (idx *MemoryIndex) Index(ctx context.Context, doc *Document) error {
	return idx.IndexBatch(ctx, []*Document{doc})
}

// IndexBatch adds or updates a slice of documents.
func (idx *MemoryIndex) IndexBatch(ctx context.Context, docs []*Document) error {
	if len(docs) == 0 {
		return nil
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.closed {
		return ErrIndexClosed
	}

	for _, doc := range docs {
		if doc == nil {
			continue
		}
		if strings.TrimSpace(doc.ID) == "" {
			return ErrEmptyDocumentID
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// If document already exists, remove its old postings first.
		if _, exists := idx.docs[doc.ID]; exists {
			idx.removeDocPostingsLocked(doc.ID)
		}

		// Store deep clone of document pointers/fields
		clonedDoc := &Document{
			ID:            doc.ID,
			Fields:        make(map[string]string, len(doc.Fields)),
			NumericFields: make(map[string]float64, len(doc.NumericFields)),
			Tags:          make([]string, len(doc.Tags)),
		}
		for k, v := range doc.Fields {
			clonedDoc.Fields[k] = v
		}
		for k, v := range doc.NumericFields {
			clonedDoc.NumericFields[k] = v
		}
		copy(clonedDoc.Tags, doc.Tags)

		idx.docs[doc.ID] = clonedDoc
		idx.docFieldLens[doc.ID] = make(map[string]int)

		// Index full-text fields
		for field, text := range doc.Fields {
			analyzer := idx.analyzerForField(field)
			tokens := analyzer.Analyze(text)
			fieldLen := len(tokens)
			idx.docFieldLens[doc.ID][field] = fieldLen
			idx.totalFieldLens[field] += int64(fieldLen)

			if _, ok := idx.postings[field]; !ok {
				idx.postings[field] = make(map[string]map[string]*Posting)
			}

			for _, tok := range tokens {
				term := tok.Text
				if _, ok := idx.postings[field][term]; !ok {
					idx.postings[field][term] = make(map[string]*Posting)
				}
				p, ok := idx.postings[field][term][doc.ID]
				if !ok {
					p = &Posting{
						DocID:     doc.ID,
						TermFreq:  0,
						Positions: make([]int, 0),
					}
					idx.postings[field][term][doc.ID] = p
				}
				p.TermFreq++
				p.Positions = append(p.Positions, tok.Position)
			}
		}

		// Index tags
		for _, tag := range doc.Tags {
			if _, ok := idx.tagIndex[tag]; !ok {
				idx.tagIndex[tag] = make(map[string]struct{})
			}
			idx.tagIndex[tag][doc.ID] = struct{}{}
		}
	}

	return nil
}

// removeDocPostingsLocked removes all indexed terms and tags for a given doc ID.
func (idx *MemoryIndex) removeDocPostingsLocked(docID string) {
	oldLens := idx.docFieldLens[docID]
	for field, length := range oldLens {
		idx.totalFieldLens[field] -= int64(length)
		if fieldPostings, ok := idx.postings[field]; ok {
			for term, docMap := range fieldPostings {
				delete(docMap, docID)
				if len(docMap) == 0 {
					delete(fieldPostings, term)
				}
			}
		}
	}
	delete(idx.docFieldLens, docID)

	// Remove from tag index
	for tag, docSet := range idx.tagIndex {
		delete(docSet, docID)
		if len(docSet) == 0 {
			delete(idx.tagIndex, tag)
		}
	}

	delete(idx.docs, docID)
}

// Delete removes a document by ID.
func (idx *MemoryIndex) Delete(ctx context.Context, id string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.closed {
		return ErrIndexClosed
	}

	if _, exists := idx.docs[id]; !exists {
		return ErrDocumentNotFound
	}

	idx.removeDocPostingsLocked(id)
	return nil
}

// Get returns the stored document by ID.
func (idx *MemoryIndex) Get(ctx context.Context, id string) (*Document, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.closed {
		return nil, ErrIndexClosed
	}

	doc, exists := idx.docs[id]
	if !exists {
		return nil, ErrDocumentNotFound
	}

	return doc, nil
}

// DocCount returns the number of active documents in the index.
func (idx *MemoryIndex) DocCount() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.docs)
}

// Close releases the index.
func (idx *MemoryIndex) Close() error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.closed {
		return nil
	}
	idx.closed = true
	idx.docs = nil
	idx.postings = nil
	idx.docFieldLens = nil
	idx.totalFieldLens = nil
	idx.tagIndex = nil
	return nil
}

// Search evaluates a search request and returns matching documents ordered by BM25 score.
func (idx *MemoryIndex) Search(ctx context.Context, req Request) (*Result, error) {
	start := time.Now()

	if req.Query == nil {
		return nil, ErrInvalidQuery
	}
	if err := req.Query.Validate(); err != nil {
		return nil, err
	}

	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if idx.closed {
		return nil, ErrIndexClosed
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Score candidates
	scoredDocs, err := idx.evalQuery(ctx, req.Query)
	if err != nil {
		return nil, err
	}

	// Filter and assemble hits
	hits := make([]*Hit, 0, len(scoredDocs))
	for docID, score := range scoredDocs {
		if score <= 0 {
			continue
		}
		doc := idx.docs[docID]
		if doc == nil {
			continue
		}
		if req.FilterFunc != nil && !req.FilterFunc(doc) {
			continue
		}

		hit := &Hit{
			DocID:    docID,
			Score:    score,
			Document: doc,
		}

		if len(req.HighlightFields) > 0 {
			hit.Highlights = idx.generateHighlights(doc, req.Query, req.HighlightFields)
		}

		hits = append(hits, hit)
	}

	// Sort hits descending by score, tie-breaking by DocID
	sort.Slice(hits, func(i, j int) bool {
		if math.Abs(hits[i].Score-hits[j].Score) > 1e-9 {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].DocID < hits[j].DocID
	})

	totalHits := len(hits)

	// Apply pagination (Limit / Offset)
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	pagedHits := make([]*Hit, 0)
	if offset < totalHits {
		end := offset + limit
		if end > totalHits {
			end = totalHits
		}
		pagedHits = hits[offset:end]
	}

	return &Result{
		TotalHits: totalHits,
		Hits:      pagedHits,
		Took:      time.Since(start),
	}, nil
}

// evalQuery routes query evaluation to specific query processors.
func (idx *MemoryIndex) evalQuery(ctx context.Context, q Query) (map[string]float64, error) {
	switch query := q.(type) {
	case *TermQuery:
		return idx.evalTermQuery(query), nil
	case *MatchQuery:
		return idx.evalMatchQuery(query), nil
	case *MultiMatchQuery:
		return idx.evalMultiMatchQuery(query), nil
	case *BoolQuery:
		return idx.evalBoolQuery(ctx, query)
	case *PrefixQuery:
		return idx.evalPrefixQuery(query), nil
	case *FuzzyQuery:
		return idx.evalFuzzyQuery(query), nil
	case *PhraseQuery:
		return idx.evalPhraseQuery(query), nil
	case *RangeQuery:
		return idx.evalRangeQuery(query), nil
	case *TagQuery:
		return idx.evalTagQuery(query), nil
	default:
		return nil, ErrInvalidQuery
	}
}

// bm25Score computes the Okapi BM25 score for a single term in a field.
func (idx *MemoryIndex) bm25Score(termFreq int, fieldLen int, avgFieldLen float64, totalDocs int, docFreq int) float64 {
	if docFreq <= 0 || totalDocs <= 0 || termFreq <= 0 {
		return 0
	}
	// Robertson-Spärck Jones IDF with floor
	idf := math.Log(1.0 + (float64(totalDocs)-float64(docFreq)+0.5)/(float64(docFreq)+0.5))
	if idf < 0.05 {
		idf = 0.05
	}

	lenNorm := 1.0 - idx.cfg.B + idx.cfg.B*(float64(fieldLen)/avgFieldLen)
	tfNorm := float64(termFreq) * (idx.cfg.K1 + 1.0) / (float64(termFreq) + idx.cfg.K1*lenNorm)

	return idf * tfNorm
}

// evalTermQuery scores docs matching a single exact term in a field.
func (idx *MemoryIndex) evalTermQuery(q *TermQuery) map[string]float64 {
	scores := make(map[string]float64)
	fieldPostings, ok := idx.postings[q.Field]
	if !ok {
		return scores
	}

	docPostings, ok := fieldPostings[q.Term]
	if !ok {
		return scores
	}

	totalDocs := len(idx.docs)
	docFreq := len(docPostings)
	avgFieldLen := 1.0
	if totalDocs > 0 {
		avgFieldLen = math.Max(1.0, float64(idx.totalFieldLens[q.Field])/float64(totalDocs))
	}

	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	for docID, posting := range docPostings {
		fieldLen := idx.docFieldLens[docID][q.Field]
		score := idx.bm25Score(posting.TermFreq, fieldLen, avgFieldLen, totalDocs, docFreq)
		scores[docID] = score * boost
	}

	return scores
}

// evalMatchQuery analyzes text and performs BM25 scoring with AND/OR coordination.
func (idx *MemoryIndex) evalMatchQuery(q *MatchQuery) map[string]float64 {
	analyzer := q.Analyzer
	if analyzer == nil {
		analyzer = idx.analyzerForField(q.Field)
	}

	tokens := analyzer.Analyze(q.Text)
	if len(tokens) == 0 {
		return make(map[string]float64)
	}

	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	// Distinct terms
	termTokens := make(map[string]struct{})
	for _, tok := range tokens {
		termTokens[tok.Text] = struct{}{}
	}

	totalDocs := len(idx.docs)
	avgFieldLen := 1.0
	if totalDocs > 0 {
		avgFieldLen = math.Max(1.0, float64(idx.totalFieldLens[q.Field])/float64(totalDocs))
	}

	fieldPostings := idx.postings[q.Field]
	if fieldPostings == nil {
		return make(map[string]float64)
	}

	termMatchCounts := make(map[string]int) // docID -> count of matched terms
	docScores := make(map[string]float64)

	for term := range termTokens {
		docMap, ok := fieldPostings[term]
		if !ok {
			continue
		}
		docFreq := len(docMap)
		for docID, posting := range docMap {
			fieldLen := idx.docFieldLens[docID][q.Field]
			s := idx.bm25Score(posting.TermFreq, fieldLen, avgFieldLen, totalDocs, docFreq)
			docScores[docID] += s * boost
			termMatchCounts[docID]++
		}
	}

	// If operator is AND, retain only documents that matched all terms
	if q.Operator == "AND" {
		requiredTerms := len(termTokens)
		for docID, count := range termMatchCounts {
			if count < requiredTerms {
				delete(docScores, docID)
			}
		}
	}

	return docScores
}

// evalMultiMatchQuery evaluates a match query across multiple fields.
func (idx *MemoryIndex) evalMultiMatchQuery(q *MultiMatchQuery) map[string]float64 {
	scores := make(map[string]float64)
	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	for _, field := range q.Fields {
		fieldBoost := 1.0
		if b, ok := q.FieldBoosts[field]; ok && b > 0 {
			fieldBoost = b
		}

		mq := NewMatchQuery(field, q.Text).
			WithOperator(q.Operator).
			WithAnalyzer(q.Analyzer).
			WithBoost(fieldBoost * boost)

		fieldScores := idx.evalMatchQuery(mq)
		for docID, s := range fieldScores {
			scores[docID] += s
		}
	}

	return scores
}

// evalBoolQuery coordinates Must, Should, MustNot, and Filter clauses.
func (idx *MemoryIndex) evalBoolQuery(ctx context.Context, q *BoolQuery) (map[string]float64, error) {
	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	scores := make(map[string]float64)

	// 1. Process Must clauses (AND logic)
	if len(q.Must) > 0 {
		for i, clause := range q.Must {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			clauseScores, err := idx.evalQuery(ctx, clause)
			if err != nil {
				return nil, err
			}
			if i == 0 {
				scores = clauseScores
			} else {
				// Intersect
				for docID := range scores {
					if cScore, ok := clauseScores[docID]; ok {
						scores[docID] += cScore
					} else {
						delete(scores, docID)
					}
				}
			}
			if len(scores) == 0 {
				break
			}
		}
	}

	// 2. Process Should clauses (OR logic)
	if len(q.Should) > 0 {
		shouldCounts := make(map[string]int)
		shouldScores := make(map[string]float64)

		for _, clause := range q.Should {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cScores, err := idx.evalQuery(ctx, clause)
			if err != nil {
				return nil, err
			}
			for docID, s := range cScores {
				shouldScores[docID] += s
				shouldCounts[docID]++
			}
		}

		minShould := q.MinimumShouldMatch
		if minShould <= 0 {
			if len(q.Must) == 0 && len(q.Filter) == 0 {
				minShould = 1
			} else {
				minShould = 0
			}
		}

		if len(q.Must) == 0 && len(q.Filter) == 0 {
			for docID, count := range shouldCounts {
				if count >= minShould {
					scores[docID] = shouldScores[docID]
				}
			}
		} else {
			// Add should score bonuses to already-qualified candidates
			for docID := range scores {
				if shouldCounts[docID] >= minShould {
					scores[docID] += shouldScores[docID]
				} else if minShould > 0 {
					delete(scores, docID)
				}
			}
		}
	}

	// 3. Process Filter clauses (Binary criteria without score impact)
	for _, clause := range q.Filter {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		filterScores, err := idx.evalQuery(ctx, clause)
		if err != nil {
			return nil, err
		}
		if len(q.Must) == 0 && len(q.Should) == 0 && len(scores) == 0 {
			// Filter was the only query
			for docID := range filterScores {
				scores[docID] = 1.0 // constant score
			}
		} else {
			for docID := range scores {
				if _, ok := filterScores[docID]; !ok {
					delete(scores, docID)
				}
			}
		}
	}

	// 4. Process MustNot clauses (Exclusion)
	for _, clause := range q.MustNot {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		excludedScores, err := idx.evalQuery(ctx, clause)
		if err != nil {
			return nil, err
		}
		for docID := range excludedScores {
			delete(scores, docID)
		}
	}

	// Apply boost
	for docID := range scores {
		scores[docID] *= boost
	}

	return scores, nil
}

// evalPrefixQuery finds all index terms starting with prefix and aggregates scores.
func (idx *MemoryIndex) evalPrefixQuery(q *PrefixQuery) map[string]float64 {
	scores := make(map[string]float64)
	fieldPostings, ok := idx.postings[q.Field]
	if !ok {
		return scores
	}

	prefix := strings.ToLower(q.Prefix)
	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	for term := range fieldPostings {
		if strings.HasPrefix(term, prefix) {
			tq := NewTermQuery(q.Field, term).WithBoost(boost)
			tScores := idx.evalTermQuery(tq)
			for docID, s := range tScores {
				scores[docID] += s
			}
		}
	}

	return scores
}

// evalFuzzyQuery matches terms within Levenshtein edit distance.
func (idx *MemoryIndex) evalFuzzyQuery(q *FuzzyQuery) map[string]float64 {
	scores := make(map[string]float64)
	fieldPostings, ok := idx.postings[q.Field]
	if !ok {
		return scores
	}

	target := strings.ToLower(q.Term)
	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	for term := range fieldPostings {
		if q.PrefixLength > 0 {
			if len(term) < q.PrefixLength || len(target) < q.PrefixLength {
				continue
			}
			if term[:q.PrefixLength] != target[:q.PrefixLength] {
				continue
			}
		}

		dist := levenshteinDistance(target, term)
		if dist <= q.MaxEdits {
			// Weight score by closeness: edit distance 0 => 1.0, 1 => 0.75, 2 => 0.5
			distanceWeight := 1.0 - (float64(dist) * 0.25)
			tq := NewTermQuery(q.Field, term).WithBoost(boost * distanceWeight)
			tScores := idx.evalTermQuery(tq)
			for docID, s := range tScores {
				scores[docID] += s
			}
		}
	}

	return scores
}

// levenshteinDistance computes edit distance between two strings.
func levenshteinDistance(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)
	l1, l2 := len(r1), len(r2)

	if l1 == 0 {
		return l2
	}
	if l2 == 0 {
		return l1
	}

	prev := make([]int, l2+1)
	curr := make([]int, l2+1)

	for j := 0; j <= l2; j++ {
		prev[j] = j
	}

	for i := 1; i <= l1; i++ {
		curr[0] = i
		for j := 1; j <= l2; j++ {
			cost := 0
			if r1[i-1] != r2[j-1] {
				cost = 1
			}
			curr[j] = min(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		copy(prev, curr)
	}

	return prev[l2]
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// evalPhraseQuery verifies that words appear in exact consecutive order within slop distance.
func (idx *MemoryIndex) evalPhraseQuery(q *PhraseQuery) map[string]float64 {
	scores := make(map[string]float64)
	if len(q.Terms) < 2 {
		return scores
	}

	fieldPostings, ok := idx.postings[q.Field]
	if !ok {
		return scores
	}

	analyzer := idx.analyzerForField(q.Field)
	var processedTerms []string
	for _, term := range q.Terms {
		tokens := analyzer.Analyze(term)
		for _, tok := range tokens {
			processedTerms = append(processedTerms, tok.Text)
		}
	}
	if len(processedTerms) < 2 {
		return scores
	}

	// 1. Collect postings for each term
	termPostings := make([]map[string]*Posting, len(processedTerms))
	for i, term := range processedTerms {
		docMap, ok := fieldPostings[term]
		if !ok {
			return scores // Missing any term means phrase cannot match
		}
		termPostings[i] = docMap
	}

	// 2. Candidate documents must contain all terms
	candidates := make(map[string]struct{})
	for docID := range termPostings[0] {
		candidates[docID] = struct{}{}
	}
	for i := 1; i < len(termPostings); i++ {
		for docID := range candidates {
			if _, ok := termPostings[i][docID]; !ok {
				delete(candidates, docID)
			}
		}
	}

	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	// 3. Check positions
	for docID := range candidates {
		matchCount := 0
		firstPositions := termPostings[0][docID].Positions

		for _, pos := range firstPositions {
			currPos := pos
			matched := true
			for i := 1; i < len(termPostings); i++ {
				nextPositions := termPostings[i][docID].Positions
				foundNext := false
				for _, nPos := range nextPositions {
					diff := nPos - currPos - 1
					if diff >= 0 && diff <= q.Slop {
						currPos = nPos
						foundNext = true
						break
					}
				}
				if !foundNext {
					matched = false
					break
				}
			}
			if matched {
				matchCount++
			}
		}

		if matchCount > 0 {
			scores[docID] = float64(matchCount) * 2.0 * boost
		}
	}

	return scores
}

// evalRangeQuery evaluates numeric ranges.
func (idx *MemoryIndex) evalRangeQuery(q *RangeQuery) map[string]float64 {
	scores := make(map[string]float64)
	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	for docID, doc := range idx.docs {
		val, ok := doc.GetNumeric(q.Field)
		if !ok {
			continue
		}

		match := true
		if q.GTE != nil && val < *q.GTE {
			match = false
		}
		if q.GT != nil && val <= *q.GT {
			match = false
		}
		if q.LTE != nil && val > *q.LTE {
			match = false
		}
		if q.LT != nil && val >= *q.LT {
			match = false
		}

		if match {
			scores[docID] = 1.0 * boost
		}
	}

	return scores
}

// evalTagQuery matches exact tags.
func (idx *MemoryIndex) evalTagQuery(q *TagQuery) map[string]float64 {
	scores := make(map[string]float64)
	boost := q.Boost
	if boost <= 0 {
		boost = 1.0
	}

	docSet, ok := idx.tagIndex[q.Tag]
	if !ok {
		return scores
	}

	for docID := range docSet {
		scores[docID] = 1.0 * boost
	}

	return scores
}

// generateHighlights extracts contextual snippet highlighting matching query terms.
func (idx *MemoryIndex) generateHighlights(doc *Document, q Query, fields []string) map[string][]string {
	highlights := make(map[string][]string)
	terms := extractQueryTerms(q)
	if len(terms) == 0 {
		return highlights
	}

	for _, field := range fields {
		text := doc.GetField(field)
		if text == "" {
			continue
		}

		highlighted := text
		for _, term := range terms {
			// Case-insensitive term replacement with <em>...</em>
			lowerText := strings.ToLower(highlighted)
			lowerTerm := strings.ToLower(term)
			if idx := strings.Index(lowerText, lowerTerm); idx >= 0 {
				actual := highlighted[idx : idx+len(lowerTerm)]
				highlighted = strings.ReplaceAll(highlighted, actual, "<em>"+actual+"</em>")
			}
		}

		if strings.Contains(highlighted, "<em>") {
			highlights[field] = []string{highlighted}
		}
	}

	return highlights
}

func extractQueryTerms(q Query) []string {
	var terms []string
	switch query := q.(type) {
	case *TermQuery:
		terms = append(terms, query.Term)
	case *MatchQuery:
		terms = append(terms, strings.Fields(query.Text)...)
	case *MultiMatchQuery:
		terms = append(terms, strings.Fields(query.Text)...)
	case *PrefixQuery:
		terms = append(terms, query.Prefix)
	case *FuzzyQuery:
		terms = append(terms, query.Term)
	case *PhraseQuery:
		terms = append(terms, query.Terms...)
	case *BoolQuery:
		for _, clause := range query.Must {
			terms = append(terms, extractQueryTerms(clause)...)
		}
		for _, clause := range query.Should {
			terms = append(terms, extractQueryTerms(clause)...)
		}
	}
	return terms
}
