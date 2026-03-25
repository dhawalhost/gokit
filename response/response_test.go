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

func TestOk(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	response.Ok[testData](w, r, testData{ID: 1, Name: "Alice"})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
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
}

func TestCreated(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodPost, "/", nil)
	response.Created[testData](w, r, testData{ID: 2, Name: "Bob"})

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
}

func TestNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	response.NoContent(w)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %d bytes", w.Body.Len())
	}
}

func TestJSON(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	response.JSON[testData](w, r, http.StatusAccepted, testData{ID: 3, Name: "Carol"})
	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}
}

func TestPaginated(t *testing.T) {
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	items := []testData{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}
	pg := response.Pagination{
		Page: 1, PageSize: 10, Total: 2, TotalPages: 1, HasNext: false, HasPrev: false,
	}
	response.Paginated[testData](w, r, items, pg)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
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
}
