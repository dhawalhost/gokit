package response_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dhawalhost/gokit/response"
)

type testData struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestResponse_Helpers_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		execute    func(w http.ResponseWriter, r *http.Request)
		wantStatus int
		verify     func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "Ok writes 200 with success body and JSON content type",
			execute: func(w http.ResponseWriter, r *http.Request) {
				response.Ok[testData](w, r, testData{ID: 1, Name: "Alice"})
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				if ct := w.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("expected application/json, got %q", ct)
				}
				var body response.Response[testData]
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !body.Success {
					t.Error("expected Success=true")
				}
				if body.Data.Name != "Alice" {
					t.Errorf("expected Name='Alice', got %q", body.Data.Name)
				}
			},
		},
		{
			name: "Created writes 201 with created resource data",
			execute: func(w http.ResponseWriter, r *http.Request) {
				response.Created[testData](w, r, testData{ID: 2, Name: "Bob"})
			},
			wantStatus: http.StatusCreated,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body response.Response[testData]
				_ = json.NewDecoder(w.Body).Decode(&body)
				if body.Data.ID != 2 || body.Data.Name != "Bob" {
					t.Errorf("unexpected body data: %+v", body.Data)
				}
			},
		},
		{
			name: "NoContent writes 204 with empty body",
			execute: func(w http.ResponseWriter, _ *http.Request) {
				response.NoContent(w)
			},
			wantStatus: http.StatusNoContent,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				if w.Body.Len() != 0 {
					t.Errorf("expected empty body, got %d bytes", w.Body.Len())
				}
			},
		},
		{
			name: "JSON writes custom status code with payload",
			execute: func(w http.ResponseWriter, r *http.Request) {
				response.JSON[testData](w, r, http.StatusAccepted, testData{ID: 3, Name: "Carol"})
			},
			wantStatus: http.StatusAccepted,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body response.Response[testData]
				_ = json.NewDecoder(w.Body).Decode(&body)
				if body.Data.Name != "Carol" {
					t.Errorf("expected Name='Carol', got %q", body.Data.Name)
				}
			},
		},
		{
			name: "Paginated writes items with pagination metadata",
			execute: func(w http.ResponseWriter, r *http.Request) {
				items := []testData{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}
				pg := response.Pagination{
					Page: 1, PageSize: 10, Total: 2, TotalPages: 1, HasNext: false, HasPrev: false,
				}
				response.Paginated[testData](w, r, items, pg)
			},
			wantStatus: http.StatusOK,
			verify: func(t *testing.T, w *httptest.ResponseRecorder) {
				var body response.PaginatedResponse[testData]
				if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !body.Success {
					t.Error("expected Success=true")
				}
				if len(body.Data) != 2 {
					t.Errorf("expected 2 items, got %d", len(body.Data))
				}
				if body.Pagination.Total != 2 {
					t.Errorf("expected total=2, got %d", body.Pagination.Total)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r, _ := http.NewRequest(http.MethodGet, "/", nil)
			tc.execute(w, r)

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, w.Code)
			}
			if tc.verify != nil {
				tc.verify(t, w)
			}
		})
	}
}
