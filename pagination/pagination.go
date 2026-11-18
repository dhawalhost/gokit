// Package pagination provides helpers for offset and cursor-based pagination.
package pagination

import (
	"math"
	"net/http"
	"strconv"
)

// Pagination holds paging metadata for offset and cursor responses.
type Pagination struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
	HasNext    bool  `json:"has_next"`
	HasPrev    bool  `json:"has_prev"`
}

// OffsetParams holds parsed offset-pagination parameters.
type OffsetParams struct {
	Page     int
	PageSize int
}

// ParseOffsetParams extracts page and page_size from the request query string.
// Defaults: page=1, page_size=20.
func ParseOffsetParams(r *http.Request) OffsetParams {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return OffsetParams{Page: page, PageSize: pageSize}
}

// Offset returns the SQL OFFSET for the current page and page size.
func (p OffsetParams) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// Apply applies LIMIT and OFFSET clauses to any query builder (such as *gorm.DB)
// implementing Limit(int) T and Offset(int) T.
func Apply[T interface {
	Limit(int) T
	Offset(int) T
}](p OffsetParams, db T) T {
	return db.Limit(p.PageSize).Offset(p.Offset())
}

// ToPagination builds a Pagination from the total record count.
func (p OffsetParams) ToPagination(total int64) Pagination {
	totalPages := int(math.Ceil(float64(total) / float64(p.PageSize)))
	return Pagination{
		Page:       p.Page,
		PageSize:   p.PageSize,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    p.Page < totalPages,
		HasPrev:    p.Page > 1,
	}
}

// CursorParams holds parsed cursor-pagination parameters.
type CursorParams struct {
	Cursor    string
	Limit     int
	Direction string
}

// ParseCursorParams extracts cursor, limit, and direction from the request query string.
func ParseCursorParams(r *http.Request) CursorParams {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	dir := r.URL.Query().Get("direction")
	if dir == "" {
		dir = "next"
	}
	return CursorParams{
		Cursor:    r.URL.Query().Get("cursor"),
		Limit:     limit,
		Direction: dir,
	}
}
