package pagination_test

import (
	"net/http"
	"testing"

	"github.com/dhawalhost/gokit/pagination"
)

func makeRequest(query string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/?"+query, nil)
	return r
}

func TestParseOffsetParamsDefaults(t *testing.T) {
	p := pagination.ParseOffsetParams(makeRequest(""))
	if p.Page != 1 {
		t.Errorf("expected page=1, got %d", p.Page)
	}
	if p.PageSize != 20 {
		t.Errorf("expected page_size=20, got %d", p.PageSize)
	}
}

func TestParseOffsetParamsCustom(t *testing.T) {
	p := pagination.ParseOffsetParams(makeRequest("page=3&page_size=50"))
	if p.Page != 3 {
		t.Errorf("expected page=3, got %d", p.Page)
	}
	if p.PageSize != 50 {
		t.Errorf("expected page_size=50, got %d", p.PageSize)
	}
}

func TestParseOffsetParamsInvalidPage(t *testing.T) {
	p := pagination.ParseOffsetParams(makeRequest("page=-1"))
	if p.Page != 1 {
		t.Errorf("expected page=1 for negative input, got %d", p.Page)
	}
}

func TestParseOffsetParamsPageSizeMax(t *testing.T) {
	p := pagination.ParseOffsetParams(makeRequest("page_size=200"))
	if p.PageSize != 20 {
		t.Errorf("expected capped page_size=20, got %d", p.PageSize)
	}
}

func TestToPagination(t *testing.T) {
	p := pagination.OffsetParams{Page: 2, PageSize: 10}
	pg := p.ToPagination(35)
	if pg.TotalPages != 4 {
		t.Errorf("expected 4 total pages, got %d", pg.TotalPages)
	}
	if !pg.HasPrev {
		t.Error("expected HasPrev=true for page 2")
	}
	if !pg.HasNext {
		t.Error("expected HasNext=true on page 2 of 4")
	}
	if pg.Total != 35 {
		t.Errorf("expected total=35, got %d", pg.Total)
	}
}

func TestToPaginationLastPage(t *testing.T) {
	p := pagination.OffsetParams{Page: 4, PageSize: 10}
	pg := p.ToPagination(35)
	if pg.HasNext {
		t.Error("expected HasNext=false on last page")
	}
	if !pg.HasPrev {
		t.Error("expected HasPrev=true on page 4")
	}
}

func TestToPaginationFirstPage(t *testing.T) {
	p := pagination.OffsetParams{Page: 1, PageSize: 10}
	pg := p.ToPagination(35)
	if pg.HasPrev {
		t.Error("expected HasPrev=false on first page")
	}
}

func TestParseCursorParamsDefaults(t *testing.T) {
	cp := pagination.ParseCursorParams(makeRequest(""))
	if cp.Limit != 20 {
		t.Errorf("expected limit=20, got %d", cp.Limit)
	}
	if cp.Direction != "next" {
		t.Errorf("expected direction='next', got %q", cp.Direction)
	}
	if cp.Cursor != "" {
		t.Errorf("expected empty cursor, got %q", cp.Cursor)
	}
}

func TestParseCursorParamsCustom(t *testing.T) {
	cp := pagination.ParseCursorParams(makeRequest("cursor=abc123&limit=5&direction=prev"))
	if cp.Cursor != "abc123" {
		t.Errorf("expected cursor='abc123', got %q", cp.Cursor)
	}
	if cp.Limit != 5 {
		t.Errorf("expected limit=5, got %d", cp.Limit)
	}
	if cp.Direction != "prev" {
		t.Errorf("expected direction='prev', got %q", cp.Direction)
	}
}

func TestParseCursorParamsLimitMax(t *testing.T) {
	cp := pagination.ParseCursorParams(makeRequest("limit=999"))
	if cp.Limit != 20 {
		t.Errorf("expected capped limit=20, got %d", cp.Limit)
	}
}
