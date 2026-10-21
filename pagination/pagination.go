// Package pagination provides helpers for offset and cursor-based pagination.
package pagination

import (
	"math"
	"net/http"
	"strconv"

	"gorm.io/gorm"

	"github.com/dhawalhost/gokit/response"
)

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

// Apply applies LIMIT and OFFSET clauses to db based on the offset params.
func (p OffsetParams) Apply(db *gorm.DB) *gorm.DB {
	offset := (p.Page - 1) * p.PageSize
	return db.Limit(p.PageSize).Offset(offset)
}

// ToPagination builds a response.Pagination from the total record count.
func (p OffsetParams) ToPagination(total int64) response.Pagination {
	totalPages := int(math.Ceil(float64(total) / float64(p.PageSize)))
	return response.Pagination{
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
