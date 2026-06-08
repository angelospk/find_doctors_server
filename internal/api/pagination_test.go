package api

import (
	"net/http/httptest"
	"testing"
)

func TestPagination_Defaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	limit, offset := pagination(r)
	if limit != 50 || offset != 0 {
		t.Errorf("expected (50,0), got (%d,%d)", limit, offset)
	}
}

func TestPagination_CapsAt200(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?limit=1000&offset=10", nil)
	limit, offset := pagination(r)
	if limit != 200 || offset != 10 {
		t.Errorf("expected (200,10), got (%d,%d)", limit, offset)
	}
}

func TestPagination_RejectsNegative(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?limit=-5&offset=-3", nil)
	limit, offset := pagination(r)
	// Negative values are ignored — defaults stand.
	if limit != 50 || offset != 0 {
		t.Errorf("expected defaults on negative, got (%d,%d)", limit, offset)
	}
}

func TestPaginate_OffsetBeyondLength(t *testing.T) {
	xs := []int{1, 2, 3}
	got := paginate(xs, 10, 99)
	if len(got) != 0 {
		t.Errorf("expected empty slice when offset > len, got %v", got)
	}
}

func TestPaginate_NormalSlice(t *testing.T) {
	xs := []int{1, 2, 3, 4, 5}
	got := paginate(xs, 2, 1)
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Errorf("expected [2 3], got %v", got)
	}
}
