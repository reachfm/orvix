package kernel

import "strings"

// PageRequest is the canonical pagination/sort/filter input every
// platform list endpoint accepts. It is intentionally offset-based (not
// cursor-based) to match the existing admin console's list UIs, which
// already page by number; a cursor variant can be added later without
// breaking this struct's JSON shape.
type PageRequest struct {
	Page     int    // 1-based; 0 or negative normalizes to 1
	PageSize int    // normalizes to DefaultPageSize..MaxPageSize
	SortBy   string // caller-validated against an allowlist; kernel does not know column names
	SortDesc bool
	Search   string
}

const (
	DefaultPageSize = 25
	MaxPageSize     = 200
)

// Normalize clamps Page/PageSize into a safe range. Callers must call this
// before using the values to build a SQL LIMIT/OFFSET — never trust raw
// client-supplied page/page_size directly, since an unbounded page_size is
// a denial-of-service vector against the database.
func (p PageRequest) Normalize() PageRequest {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	p.Search = strings.TrimSpace(p.Search)
	return p
}

func (p PageRequest) Offset() int {
	return (p.Page - 1) * p.PageSize
}

func (p PageRequest) Limit() int {
	return p.PageSize
}

// PageResponse is the canonical envelope for a paginated list response.
type PageResponse[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalCount int `json:"total_count"`
	TotalPages int `json:"total_pages"`
}

func NewPageResponse[T any](items []T, req PageRequest, totalCount int) PageResponse[T] {
	totalPages := 0
	if req.PageSize > 0 {
		totalPages = (totalCount + req.PageSize - 1) / req.PageSize
	}
	if items == nil {
		items = []T{}
	}
	return PageResponse[T]{
		Items:      items,
		Page:       req.Page,
		PageSize:   req.PageSize,
		TotalCount: totalCount,
		TotalPages: totalPages,
	}
}

// AllowlistSort returns sortBy if it is present in allowed, else fallback.
// Every repository must run its SortBy input through this before
// interpolating it into an ORDER BY clause — SQL identifiers can never be
// parameterized, so an unvalidated column name is a SQL-injection vector.
func AllowlistSort(sortBy string, allowed map[string]string, fallback string) string {
	if col, ok := allowed[sortBy]; ok {
		return col
	}
	return fallback
}
