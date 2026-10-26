package search_test

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/dhawalhost/gokit/search"
)

func TestDocument_Basics(t *testing.T) {
	doc := search.NewDocument("doc_1")
	if doc.ID != "doc_1" {
		t.Fatalf("expected ID doc_1, got %s", doc.ID)
	}

	doc.SetField("title", "Distributed Systems in Go")
	if val := doc.GetField("title"); val != "Distributed Systems in Go" {
		t.Fatalf("unexpected field value: %s", val)
	}
	if val := doc.GetField("missing"); val != "" {
		t.Fatalf("expected empty for missing field, got %s", val)
	}

	doc.SetNumeric("price", 49.99)
	if val, ok := doc.GetNumeric("price"); !ok || math.Abs(val-49.99) > 1e-6 {
		t.Fatalf("unexpected numeric value: %f, ok: %v", val, ok)
	}
	if _, ok := doc.GetNumeric("missing"); ok {
		t.Fatal("expected ok=false for missing numeric field")
	}

	doc.AddTag("tech", "golang", "backend")
	if !doc.HasTag("golang") {
		t.Fatal("expected doc to have tag golang")
	}
	if doc.HasTag("python") {
		t.Fatal("expected doc not to have tag python")
	}
}

func TestAnalyzer_Tokenizers(t *testing.T) {
	t.Run("StandardTokenizer", func(t *testing.T) {
		tok := search.NewStandardTokenizer()
		tokens := tok.Tokenize("Hello, World! High-performance Go.")
		expected := []string{"Hello", "World", "High", "performance", "Go"}
		if len(tokens) != len(expected) {
			t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
		}
		for i, exp := range expected {
			if tokens[i].Text != exp {
				t.Errorf("token %d: expected %s, got %s", i, exp, tokens[i].Text)
			}
		}
	})

	t.Run("WhitespaceTokenizer", func(t *testing.T) {
		tok := search.NewWhitespaceTokenizer()
		tokens := tok.Tokenize("Hello,   World!\tGo\n")
		expected := []string{"Hello,", "World!", "Go"}
		if len(tokens) != len(expected) {
			t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
		}
		for i, exp := range expected {
			if tokens[i].Text != exp {
				t.Errorf("token %d: expected %s, got %s", i, exp, tokens[i].Text)
			}
		}
	})

	t.Run("EdgeNGramTokenizer", func(t *testing.T) {
		tok := search.NewEdgeNGramTokenizer(2, 4)
		tokens := tok.Tokenize("search")
		expected := []string{"se", "sea", "sear"}
		if len(tokens) != len(expected) {
			t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
		}
		for i, exp := range expected {
			if tokens[i].Text != exp {
				t.Errorf("token %d: expected %s, got %s", i, exp, tokens[i].Text)
			}
		}
	})
}

func TestAnalyzer_Filters(t *testing.T) {
	t.Run("ASCIIFold and Lowercase", func(t *testing.T) {
		a := search.NewSimpleAnalyzer()
		tokens := a.Analyze("Café CRÈME Mañana Über")
		expected := []string{"cafe", "creme", "manana", "uber"}
		if len(tokens) != len(expected) {
			t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
		}
		for i, exp := range expected {
			if tokens[i].Text != exp {
				t.Errorf("token %d: expected %s, got %s", i, exp, tokens[i].Text)
			}
		}
	})

	t.Run("StopWords and Stemmer", func(t *testing.T) {
		a := search.NewStandardAnalyzer()
		// "the", "and", "is" are stop words; "running" stems to "run", "cats" stems to "cat"
		tokens := a.Analyze("The cats and dogs are running quickly")
		expected := []string{"cat", "dog", "run", "quick"}
		if len(tokens) != len(expected) {
			t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
		}
		for i, exp := range expected {
			if tokens[i].Text != exp {
				t.Errorf("token %d: expected %s, got %s", i, exp, tokens[i].Text)
			}
		}
	})

	t.Run("MinLengthFilter", func(t *testing.T) {
		filter := search.MinLengthFilter(3)
		toks := []*search.Token{{Text: "a"}, {Text: "go"}, {Text: "cat"}}
		res := filter.Filter(toks)
		if len(res) != 1 || res[0].Text != "cat" {
			t.Fatalf("expected only 'cat', got %v", res)
		}
	})
}

func TestIndex_CRUD(t *testing.T) {
	ctx := context.Background()
	idx := search.NewMemoryIndex()
	defer idx.Close()

	if idx.DocCount() != 0 {
		t.Fatalf("expected 0 docs initially, got %d", idx.DocCount())
	}

	doc1 := search.NewDocument("d1").SetField("body", "Microservice architecture in Golang")
	if err := idx.Index(ctx, doc1); err != nil {
		t.Fatalf("index failed: %v", err)
	}

	if idx.DocCount() != 1 {
		t.Fatalf("expected 1 doc, got %d", idx.DocCount())
	}

	// Retrieve doc
	retrieved, err := idx.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if retrieved.GetField("body") != "Microservice architecture in Golang" {
		t.Fatalf("retrieved content mismatch")
	}

	// Update existing doc
	doc1Updated := search.NewDocument("d1").SetField("body", "Microservice and cloud in Golang")
	if err := idx.Index(ctx, doc1Updated); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if idx.DocCount() != 1 {
		t.Fatalf("expected doc count to remain 1 after update, got %d", idx.DocCount())
	}

	// Delete
	if err := idx.Delete(ctx, "d1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if idx.DocCount() != 0 {
		t.Fatalf("expected 0 docs after delete, got %d", idx.DocCount())
	}

	// Delete non-existent
	if err := idx.Delete(ctx, "non_existent"); err != search.ErrDocumentNotFound {
		t.Fatalf("expected ErrDocumentNotFound, got %v", err)
	}

	// Empty ID validation
	if err := idx.Index(ctx, search.NewDocument("")); err != search.ErrEmptyDocumentID {
		t.Fatalf("expected ErrEmptyDocumentID, got %v", err)
	}
}

func seedTestIndex(t *testing.T) *search.MemoryIndex {
	t.Helper()
	idx := search.NewMemoryIndex(search.IndexConfig{
		K1: 1.2,
		B:  0.75,
	})

	docs := []*search.Document{
		search.NewDocument("doc_1").
			SetField("title", "Go Concurrency Patterns and Goroutines").
			SetField("content", "Learn pipelines, worker pools, and channels in Go programming.").
			SetNumeric("rating", 4.9).
			SetNumeric("views", 12000).
			AddTag("go", "concurrency", "backend"),

		search.NewDocument("doc_2").
			SetField("title", "Distributed Cache Architecture with Redis").
			SetField("content", "Designing high-performance two-tier L1/L2 caches and cache invalidation.").
			SetNumeric("rating", 4.7).
			SetNumeric("views", 8500).
			AddTag("redis", "cache", "backend"),

		search.NewDocument("doc_3").
			SetField("title", "Building Event-Driven Systems with Kafka and RabbitMQ").
			SetField("content", "Streaming broker architectures, consumer groups, and eventbus patterns in Go.").
			SetNumeric("rating", 4.8).
			SetNumeric("views", 15000).
			AddTag("kafka", "rabbitmq", "events"),

		search.NewDocument("doc_4").
			SetField("title", "Database Schema Migrations and Zero Downtime").
			SetField("content", "Transactional DDL, rolling updates, and distributed locks for PostgreSQL.").
			SetNumeric("rating", 4.5).
			SetNumeric("views", 4300).
			AddTag("database", "postgres"),
	}

	if err := idx.IndexBatch(context.Background(), docs); err != nil {
		t.Fatalf("failed to seed test docs: %v", err)
	}

	return idx
}

func TestIndex_Search_MatchQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	t.Run("Match single term OR", func(t *testing.T) {
		res, err := idx.Search(ctx, search.Request{
			Query: search.NewMatchQuery("content", "worker pools"),
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if res.TotalHits == 0 {
			t.Fatal("expected at least 1 hit")
		}
		if res.Hits[0].DocID != "doc_1" {
			t.Fatalf("expected doc_1 to be highest ranked, got %s", res.Hits[0].DocID)
		}
	})

	t.Run("Match AND operator", func(t *testing.T) {
		res, err := idx.Search(ctx, search.Request{
			Query: search.NewMatchQuery("content", "cache invalidation").WithOperator("AND"),
		})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if res.TotalHits != 1 || res.Hits[0].DocID != "doc_2" {
			t.Fatalf("expected exactly doc_2, got %d hits", res.TotalHits)
		}
	})
}

func TestIndex_Search_MultiMatchQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// "Kafka" is in title of doc_3
	q := search.NewMultiMatchQuery("Kafka", "title", "content").
		SetFieldBoost("title", 3.0).
		SetFieldBoost("content", 1.0)

	res, err := idx.Search(ctx, search.Request{
		Query: q,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if res.TotalHits == 0 || res.Hits[0].DocID != "doc_3" {
		t.Fatalf("expected doc_3 as top hit, got %v", res.Hits)
	}
}

func TestIndex_Search_BoolQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	t.Run("Must AND MustNot", func(t *testing.T) {
		// Must match "Go" in content, MustNot match "Kafka" in title
		bq := search.NewBoolQuery().
			MustClause(search.NewMatchQuery("content", "Go")).
			MustNotClause(search.NewMatchQuery("title", "Kafka"))

		res, err := idx.Search(ctx, search.Request{Query: bq})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}

		for _, hit := range res.Hits {
			if hit.DocID == "doc_3" {
				t.Fatalf("doc_3 should have been excluded by MustNot clause")
			}
		}
	})

	t.Run("Filter clause", func(t *testing.T) {
		// Search "architecture" in title with Filter on tag "backend"
		bq := search.NewBoolQuery().
			MustClause(search.NewMatchQuery("title", "architecture")).
			FilterClause(search.NewTagQuery("backend"))

		res, err := idx.Search(ctx, search.Request{Query: bq})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}

		if res.TotalHits != 1 || res.Hits[0].DocID != "doc_2" {
			t.Fatalf("expected doc_2 matching backend tag, got %d hits", res.TotalHits)
		}
	})
}

func TestIndex_Search_FuzzyQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// Typo "concurrensy" instead of "concurrency" (edit distance 1)
	fuzzy := search.NewFuzzyQuery("title", "concurrensy").WithMaxEdits(1)
	res, err := idx.Search(ctx, search.Request{Query: fuzzy})
	if err != nil {
		t.Fatalf("fuzzy search failed: %v", err)
	}

	if res.TotalHits == 0 || res.Hits[0].DocID != "doc_1" {
		t.Fatalf("expected doc_1 hit on fuzzy search, got %d hits", res.TotalHits)
	}
}

func TestIndex_Search_PrefixQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// Prefix "postgre" matching "postgresql" in content of doc_4
	prefixQ := search.NewPrefixQuery("content", "postgre")
	res, err := idx.Search(ctx, search.Request{Query: prefixQ})
	if err != nil {
		t.Fatalf("prefix search failed: %v", err)
	}

	if res.TotalHits == 0 || res.Hits[0].DocID != "doc_4" {
		t.Fatalf("expected doc_4 on prefix query, got %d hits", res.TotalHits)
	}
}

func TestIndex_Search_PhraseQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// Phrase: "worker pools" in content of doc_1
	phraseQ := search.NewPhraseQuery("content", "worker", "pools")
	res, err := idx.Search(ctx, search.Request{Query: phraseQ})
	if err != nil {
		t.Fatalf("phrase search failed: %v", err)
	}

	if res.TotalHits != 1 || res.Hits[0].DocID != "doc_1" {
		t.Fatalf("expected doc_1 on phrase query, got %d hits", res.TotalHits)
	}

	// Non-matching phrase
	nonMatch := search.NewPhraseQuery("content", "pools", "worker")
	resNon, err := idx.Search(ctx, search.Request{Query: nonMatch})
	if err != nil {
		t.Fatalf("nonMatch search failed: %v", err)
	}
	if resNon.TotalHits != 0 {
		t.Fatalf("expected 0 hits for reversed phrase, got %d", resNon.TotalHits)
	}
}

func TestIndex_Search_RangeQuery(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// rating >= 4.8
	rq := search.NewRangeQuery("rating").GreaterThanOrEqual(4.8)
	res, err := idx.Search(ctx, search.Request{Query: rq})
	if err != nil {
		t.Fatalf("range query failed: %v", err)
	}

	// doc_1 (4.9) and doc_3 (4.8)
	if res.TotalHits != 2 {
		t.Fatalf("expected 2 hits with rating >= 4.8, got %d", res.TotalHits)
	}
}

func TestIndex_Pagination_And_Highlighting(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	q := search.NewMatchQuery("content", "Go")
	res, err := idx.Search(ctx, search.Request{
		Query:           q,
		Limit:           1,
		Offset:          0,
		HighlightFields: []string{"content"},
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit due to limit=1, got %d", len(res.Hits))
	}
	if res.TotalHits < 2 {
		t.Fatalf("expected total hits >= 2, got %d", res.TotalHits)
	}

	// Check highlighting snippet
	hl := res.Hits[0].Highlights["content"]
	if len(hl) == 0 {
		t.Fatal("expected highlights for content field")
	}
	if !bytes.Contains([]byte(hl[0]), []byte("<em>")) {
		t.Fatalf("expected <em> in highlight snippet: %s", hl[0])
	}
}

func TestIndex_Concurrency(t *testing.T) {
	ctx := context.Background()
	idx := search.NewMemoryIndex()
	defer idx.Close()

	var wg sync.WaitGroup
	numWorkers := 10
	docsPerWorker := 20

	// Concurrent writes
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < docsPerWorker; i++ {
				docID := fmt.Sprintf("doc_%d_%d", workerID, i)
				doc := search.NewDocument(docID).
					SetField("title", fmt.Sprintf("Article %d by author %d", i, workerID)).
					SetField("content", "Concurrent indexing and retrieval test in Go microservices").
					SetNumeric("index", float64(i))
				_ = idx.Index(ctx, doc)
			}
		}(w)
	}

	// Concurrent reads and searches
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				_, _ = idx.Search(ctx, search.Request{
					Query: search.NewMatchQuery("content", "indexing"),
					Limit: 5,
				})
			}
		}()
	}

	wg.Wait()

	expectedTotal := numWorkers * docsPerWorker
	if idx.DocCount() != expectedTotal {
		t.Fatalf("expected %d documents, got %d", expectedTotal, idx.DocCount())
	}
}

func TestSnapshot_And_Restore(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	var buf bytes.Buffer
	if err := search.Snapshot(ctx, idx, &buf); err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	// Restore into a fresh index
	freshIdx := search.NewMemoryIndex()
	defer freshIdx.Close()

	if err := search.Restore(ctx, freshIdx, &buf); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if freshIdx.DocCount() != idx.DocCount() {
		t.Fatalf("doc count mismatch: expected %d, got %d", idx.DocCount(), freshIdx.DocCount())
	}

	// Perform search on restored index
	res, err := freshIdx.Search(ctx, search.Request{
		Query: search.NewMatchQuery("title", "Kafka"),
	})
	if err != nil {
		t.Fatalf("search on restored index failed: %v", err)
	}
	if res.TotalHits == 0 || res.Hits[0].DocID != "doc_3" {
		t.Fatalf("expected doc_3 in restored index search")
	}
}

func TestIndex_Closed_And_Errors(t *testing.T) {
	ctx := context.Background()
	idx := search.NewMemoryIndex()

	if err := idx.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Operations on closed index
	if err := idx.Index(ctx, search.NewDocument("d1")); err != search.ErrIndexClosed {
		t.Fatalf("expected ErrIndexClosed, got %v", err)
	}
	if _, err := idx.Get(ctx, "d1"); err != search.ErrIndexClosed {
		t.Fatalf("expected ErrIndexClosed on get, got %v", err)
	}
	if err := idx.Delete(ctx, "d1"); err != search.ErrIndexClosed {
		t.Fatalf("expected ErrIndexClosed on delete, got %v", err)
	}
	if _, err := idx.Search(ctx, search.Request{Query: search.NewTermQuery("f", "t")}); err != search.ErrIndexClosed {
		t.Fatalf("expected ErrIndexClosed on search, got %v", err)
	}
	if err := search.Snapshot(ctx, idx, &bytes.Buffer{}); err != search.ErrIndexClosed {
		t.Fatalf("expected ErrIndexClosed on snapshot, got %v", err)
	}
	if err := search.Restore(ctx, idx, bytes.NewReader([]byte("{}"))); err != search.ErrIndexClosed {
		t.Fatalf("expected ErrIndexClosed on restore, got %v", err)
	}
	// Close idempotency
	if err := idx.Close(); err != nil {
		t.Fatalf("idempotent close failed: %v", err)
	}
}

func TestQuery_Validation(t *testing.T) {
	// TermQuery
	if err := search.NewTermQuery("", "val").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField, got %v", err)
	}
	if err := search.NewTermQuery("field", "").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}

	// MatchQuery
	if err := search.NewMatchQuery("", "val").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField, got %v", err)
	}
	if err := search.NewMatchQuery("field", "").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}

	// MultiMatchQuery
	if err := search.NewMultiMatchQuery("val").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField on empty fields, got %v", err)
	}
	if err := search.NewMultiMatchQuery("", "f1").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery on empty text, got %v", err)
	}

	// BoolQuery
	if err := search.NewBoolQuery().Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery on empty bool query, got %v", err)
	}
	if err := search.NewBoolQuery().MustClause(search.NewTermQuery("", "")).Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected nested validation error, got %v", err)
	}
	if err := search.NewBoolQuery().MustClause(nil).Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery on nil clause, got %v", err)
	}

	// PrefixQuery
	if err := search.NewPrefixQuery("", "pre").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField, got %v", err)
	}
	if err := search.NewPrefixQuery("field", "").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}

	// FuzzyQuery
	if err := search.NewFuzzyQuery("", "term").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField, got %v", err)
	}
	if err := search.NewFuzzyQuery("field", "").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery, got %v", err)
	}

	// PhraseQuery
	if err := search.NewPhraseQuery("", "w1", "w2").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField, got %v", err)
	}
	if err := search.NewPhraseQuery("field", "only_one").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery on < 2 terms, got %v", err)
	}

	// RangeQuery
	if err := search.NewRangeQuery("").Validate(); err != search.ErrInvalidField {
		t.Fatalf("expected ErrInvalidField, got %v", err)
	}
	if err := search.NewRangeQuery("price").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery with no bounds, got %v", err)
	}

	// TagQuery
	if err := search.NewTagQuery("").Validate(); err != search.ErrInvalidQuery {
		t.Fatalf("expected ErrInvalidQuery on empty tag, got %v", err)
	}
}

func TestQuery_Options_And_Boosts(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// TermQuery with boost
	tq := search.NewTermQuery("content", "go").WithBoost(2.5)
	res, err := idx.Search(ctx, search.Request{Query: tq})
	if err != nil || res.TotalHits == 0 {
		t.Fatalf("boosted term query failed: %v", err)
	}

	// MultiMatch options
	mmq := search.NewMultiMatchQuery("Cache", "title", "content").
		WithOperator("AND").
		WithAnalyzer(search.NewStandardAnalyzer()).
		WithBoost(1.5)
	resMM, err := idx.Search(ctx, search.Request{Query: mmq})
	if err != nil || resMM.TotalHits == 0 {
		t.Fatalf("multi match options failed: %v", err)
	}

	// FuzzyQuery with prefix length and custom max edits
	fq := search.NewFuzzyQuery("title", "goroutine").
		WithMaxEdits(2).
		WithPrefixLength(3).
		WithBoost(1.2)
	resFQ, err := idx.Search(ctx, search.Request{Query: fq})
	if err != nil || resFQ.TotalHits == 0 {
		t.Fatalf("fuzzy query with options failed: %v", err)
	}

	// Fuzzy edge edits
	fqEdge := search.NewFuzzyQuery("title", "test").WithMaxEdits(0).WithMaxEdits(5)
	if fqEdge.MaxEdits != 2 {
		t.Fatalf("expected max edits clamped to 2, got %d", fqEdge.MaxEdits)
	}

	// PhraseQuery with slop and boost
	pq := search.NewPhraseQuery("title", "Concurrency", "Patterns").
		WithSlop(2).
		WithBoost(2.0)
	resPQ, err := idx.Search(ctx, search.Request{Query: pq})
	if err != nil || resPQ.TotalHits == 0 {
		t.Fatalf("phrase query with slop failed: %v", err)
	}

	// RangeQuery all bounds (GT, LT, LTE, GTE)
	rq := search.NewRangeQuery("views").
		GreaterThan(4300).
		LessThan(15000).
		WithBoost(1.5)
	resRQ, err := idx.Search(ctx, search.Request{Query: rq})
	if err != nil {
		t.Fatalf("range query with GT/LT failed: %v", err)
	}
	// doc_1 has 12000, doc_2 has 8500
	if resRQ.TotalHits != 2 {
		t.Fatalf("expected 2 hits for views (4300, 15000), got %d", resRQ.TotalHits)
	}

	rqLTE := search.NewRangeQuery("views").LessThanOrEqual(4300)
	resLTE, err := idx.Search(ctx, search.Request{Query: rqLTE})
	if err != nil || resLTE.TotalHits != 1 {
		t.Fatalf("expected 1 hit for views <= 4300, got %d", resLTE.TotalHits)
	}

	// TagQuery with boost
	tagQ := search.NewTagQuery("backend").WithBoost(1.8)
	resTag, err := idx.Search(ctx, search.Request{Query: tagQ})
	if err != nil || resTag.TotalHits != 2 {
		t.Fatalf("expected 2 hits for backend tag, got %d", resTag.TotalHits)
	}

	// PrefixQuery with boost
	prefQ := search.NewPrefixQuery("title", "build").WithBoost(2.0)
	resPref, err := idx.Search(ctx, search.Request{Query: prefQ})
	if err != nil || resPref.TotalHits == 0 {
		t.Fatalf("expected prefix hit, got %d", resPref.TotalHits)
	}
}

func TestBoolQuery_Should_Scoring(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// Should only: at least 1 should match
	bq := search.NewBoolQuery().
		ShouldClause(
			search.NewMatchQuery("title", "Concurrency"),
			search.NewMatchQuery("title", "Kafka"),
		).
		SetMinimumShouldMatch(1).
		WithBoost(1.5)

	res, err := idx.Search(ctx, search.Request{Query: bq})
	if err != nil {
		t.Fatalf("bool should query failed: %v", err)
	}
	// doc_1 has Concurrency, doc_3 has Kafka
	if res.TotalHits != 2 {
		t.Fatalf("expected 2 hits for should query, got %d", res.TotalHits)
	}

	// Should requiring 2 matches
	bqStrict := search.NewBoolQuery().
		ShouldClause(
			search.NewMatchQuery("title", "Concurrency"),
			search.NewMatchQuery("title", "Kafka"),
		).
		SetMinimumShouldMatch(2)

	resStrict, err := idx.Search(ctx, search.Request{Query: bqStrict})
	if err != nil {
		t.Fatalf("strict should query failed: %v", err)
	}
	if resStrict.TotalHits != 0 {
		t.Fatalf("expected 0 hits requiring both, got %d", resStrict.TotalHits)
	}
}

func TestAutocompleteAnalyzer_And_Tokenizers(t *testing.T) {
	// NewEdgeNGramTokenizer boundaries
	tok := search.NewEdgeNGramTokenizer(0, 1)
	if tok.MinGram != 1 {
		t.Fatalf("expected minGram clamped to 1, got %d", tok.MinGram)
	}
	tok2 := search.NewEdgeNGramTokenizer(5, 2)
	if tok2.MaxGram != 5 {
		t.Fatalf("expected maxGram clamped to minGram (5), got %d", tok2.MaxGram)
	}

	// AutocompleteAnalyzer
	autoAnalyzer := search.NewAutocompleteAnalyzer(2, 4)
	tokens := autoAnalyzer.Analyze("Gokit")
	// "go", "gok", "goki"
	if len(tokens) != 3 {
		t.Fatalf("expected 3 edge n-grams, got %d: %v", len(tokens), tokens)
	}
	if tokens[0].Text != "go" || tokens[1].Text != "gok" || tokens[2].Text != "goki" {
		t.Fatalf("unexpected tokens: %v", tokens)
	}
}

func TestIndex_CustomFieldAnalyzers(t *testing.T) {
	ctx := context.Background()
	cfg := search.IndexConfig{
		DefaultAnalyzer: search.NewStandardAnalyzer(),
		FieldAnalyzers: map[string]*search.Analyzer{
			"code": search.NewSimpleAnalyzer(),
		},
		K1: 1.5,
		B:  0.8,
	}

	idx := search.NewMemoryIndex(cfg)
	defer idx.Close()

	doc := search.NewDocument("d1").
		SetField("code", "func RunMigrations_V2()").
		SetField("title", "Database Migrations")
	if err := idx.Index(ctx, doc); err != nil {
		t.Fatalf("index with custom analyzer failed: %v", err)
	}

	res, err := idx.Search(ctx, search.Request{
		Query: search.NewMatchQuery("code", "RunMigrations_V2()"),
	})
	if err != nil || res.TotalHits == 0 {
		t.Fatalf("search with field analyzer failed: %v", err)
	}
}

func TestIndex_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel context

	idx := search.NewMemoryIndex()
	defer idx.Close()

	// Cancelled Index
	if err := idx.Index(ctx, search.NewDocument("d1").SetField("t", "test")); err != context.Canceled {
		t.Fatalf("expected context.Canceled on Index, got %v", err)
	}

	// Cancelled Search
	if _, err := idx.Search(ctx, search.Request{Query: search.NewTermQuery("t", "test")}); err != context.Canceled {
		t.Fatalf("expected context.Canceled on Search, got %v", err)
	}
}

func TestSnapshot_Restore_Errors(t *testing.T) {
	ctx := context.Background()
	idx := search.NewMemoryIndex()
	defer idx.Close()

	// Corrupted JSON restore
	corrupted := bytes.NewReader([]byte("{invalid-json"))
	if err := search.Restore(ctx, idx, corrupted); err == nil {
		t.Fatal("expected error restoring corrupted JSON")
	}
}

func TestBoolQuery_Combined_Must_And_Should_And_FilterOnly(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	// 1. Must + Should bonus
	bq := search.NewBoolQuery().
		MustClause(search.NewMatchQuery("content", "Go")).
		ShouldClause(search.NewMatchQuery("title", "Concurrency"))

	res, err := idx.Search(ctx, search.Request{Query: bq})
	if err != nil || res.TotalHits == 0 {
		t.Fatalf("must + should query failed: %v", err)
	}

	// 2. Filter-only bool query (no Must, no Should)
	filterOnly := search.NewBoolQuery().
		FilterClause(search.NewTagQuery("backend"))

	resFO, err := idx.Search(ctx, search.Request{Query: filterOnly})
	if err != nil || resFO.TotalHits != 2 {
		t.Fatalf("filter-only query failed: %v, total: %d", err, resFO.TotalHits)
	}
	// Constant score 1.0
	for _, hit := range resFO.Hits {
		if math.Abs(hit.Score-1.0) > 1e-6 {
			t.Fatalf("expected constant score 1.0 on filter only, got %f", hit.Score)
		}
	}
}

func TestHighlights_AllQueryTypes(t *testing.T) {
	ctx := context.Background()
	idx := seedTestIndex(t)
	defer idx.Close()

	queries := []search.Query{
		search.NewTermQuery("content", "worker"),
		search.NewPrefixQuery("content", "work"),
		search.NewFuzzyQuery("content", "worker"),
		search.NewPhraseQuery("content", "worker", "pools"),
		search.NewMultiMatchQuery("worker pools", "content"),
		search.NewBoolQuery().MustClause(search.NewMatchQuery("content", "worker")),
	}

	for _, q := range queries {
		res, err := idx.Search(ctx, search.Request{
			Query:           q,
			HighlightFields: []string{"content"},
		})
		if err != nil {
			t.Fatalf("highlight query failed: %v", err)
		}
		if res.TotalHits > 0 && len(res.Hits[0].Highlights["content"]) == 0 {
			t.Fatalf("expected highlights for query %T", q)
		}
	}
}

func TestASCIIFold_Comprehensive(t *testing.T) {
	a := search.NewSimpleAnalyzer()
	tokens := a.Analyze("àèìòùñçß")
	if len(tokens) == 0 {
		t.Fatal("expected tokens")
	}
	if tokens[0].Text != "aeiouncss" {
		t.Fatalf("expected aeiouncss, got %s", tokens[0].Text)
	}
}

func TestStopWords_CustomMap(t *testing.T) {
	custom := map[string]struct{}{
		"foo": {},
		"bar": {},
	}
	filter := search.StopWordsFilter(custom)
	toks := []*search.Token{{Text: "foo"}, {Text: "baz"}}
	res := filter.Filter(toks)
	if len(res) != 1 || res[0].Text != "baz" {
		t.Fatalf("expected only baz after custom stop word filter, got %v", res)
	}
}
