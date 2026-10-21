package pagination_test

import (
	"net/http"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/dhawalhost/gokit/pagination"
)

func makeRequest(query string) *http.Request {
	r, _ := http.NewRequest(http.MethodGet, "/?"+query, nil)
	return r
}

func TestOffsetParams_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		queryString  string
		wantPage     int
		wantPageSize int
	}{
		{
			name:         "empty query returns default page=1 and pageSize=20",
			queryString:  "",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "custom valid parameters are parsed correctly",
			queryString:  "page=3&page_size=50",
			wantPage:     3,
			wantPageSize: 50,
		},
		{
			name:         "negative page defaults to page=1",
			queryString:  "page=-1",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "page size exceeding maximum is capped to default 20",
			queryString:  "page_size=200",
			wantPage:     1,
			wantPageSize: 20,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := pagination.ParseOffsetParams(makeRequest(tc.queryString))
			if p.Page != tc.wantPage {
				t.Errorf("expected page=%d, got %d", tc.wantPage, p.Page)
			}
			if p.PageSize != tc.wantPageSize {
				t.Errorf("expected page_size=%d, got %d", tc.wantPageSize, p.PageSize)
			}
		})
	}
}

func TestToPagination_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		params         pagination.OffsetParams
		totalItems     int64
		wantTotalPages int
		wantHasPrev    bool
		wantHasNext    bool
	}{
		{
			name:           "middle page has both prev and next",
			params:         pagination.OffsetParams{Page: 2, PageSize: 10},
			totalItems:     35,
			wantTotalPages: 4,
			wantHasPrev:    true,
			wantHasNext:    true,
		},
		{
			name:           "first page has no prev",
			params:         pagination.OffsetParams{Page: 1, PageSize: 10},
			totalItems:     35,
			wantTotalPages: 4,
			wantHasPrev:    false,
			wantHasNext:    true,
		},
		{
			name:           "last page has no next",
			params:         pagination.OffsetParams{Page: 4, PageSize: 10},
			totalItems:     35,
			wantTotalPages: 4,
			wantHasPrev:    true,
			wantHasNext:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pg := tc.params.ToPagination(tc.totalItems)
			if pg.TotalPages != tc.wantTotalPages {
				t.Errorf("expected TotalPages=%d, got %d", tc.wantTotalPages, pg.TotalPages)
			}
			if pg.HasPrev != tc.wantHasPrev {
				t.Errorf("expected HasPrev=%v, got %v", tc.wantHasPrev, pg.HasPrev)
			}
			if pg.HasNext != tc.wantHasNext {
				t.Errorf("expected HasNext=%v, got %v", tc.wantHasNext, pg.HasNext)
			}
			if pg.Total != tc.totalItems {
				t.Errorf("expected Total=%d, got %d", tc.totalItems, pg.Total)
			}
		})
	}
}

func TestCursorParams_TableDriven(t *testing.T) {
	tests := []struct {
		name          string
		queryString   string
		wantLimit     int
		wantDirection string
		wantCursor    string
	}{
		{
			name:          "empty query returns default limit=20, direction='next', cursor=''",
			queryString:   "",
			wantLimit:     20,
			wantDirection: "next",
			wantCursor:    "",
		},
		{
			name:          "custom cursor parameters parsed accurately",
			queryString:   "cursor=abc123&limit=5&direction=prev",
			wantLimit:     5,
			wantDirection: "prev",
			wantCursor:    "abc123",
		},
		{
			name:          "excessive limit is capped to default limit=20",
			queryString:   "limit=999",
			wantLimit:     20,
			wantDirection: "next",
			wantCursor:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cp := pagination.ParseCursorParams(makeRequest(tc.queryString))
			if cp.Limit != tc.wantLimit {
				t.Errorf("expected limit=%d, got %d", tc.wantLimit, cp.Limit)
			}
			if cp.Direction != tc.wantDirection {
				t.Errorf("expected direction=%q, got %q", tc.wantDirection, cp.Direction)
			}
			if cp.Cursor != tc.wantCursor {
				t.Errorf("expected cursor=%q, got %q", tc.wantCursor, cp.Cursor)
			}
		})
	}
}

func TestApply_TableDriven(t *testing.T) {
	db := &gorm.DB{
		Config: &gorm.Config{DryRun: true},
	}
	db.Statement = &gorm.Statement{
		DB:      db,
		Clauses: map[string]clause.Clause{},
	}

	p := pagination.OffsetParams{Page: 2, PageSize: 10}
	result := p.Apply(db)
	if result == nil {
		t.Fatal("Apply returned nil")
	}
}
