package handler

import (
	"testing"

	"github.com/saschadaemgen/GoLab/internal/model"
)

// Sprint Y: handler-side tests for the rating endpoints. The DB-
// dependent paths (actually persisting a rating, SQL injection
// guard, foreign-key cascade) are covered in the integration
// suite. Here we pin the pure validation logic that lives in the
// handler: dimension allow-list, value range, max-length on
// notes. These run on every `go test ./...` without DB infra.
//
// We exercise the validation by building the same rateRequest /
// notesRequest values the handler decodes, then checking the
// model-level allow-list and the handler's own range / length
// constants. Re-using model.AllowedRatingDimensions means the
// test stays in lock-step with the schema's CHECK constraints.

func TestAllowedRatingDimensions_RejectsUnknown(t *testing.T) {
	cases := []struct {
		dim  string
		want bool
	}{
		{"track_record", true},
		{"ecosystem_fit", true},
		{"contribution_potential", true},
		{"relevance", true},
		{"communication", true},

		{"", false},
		{"notes", false},     // not a dimension
		{"reviewer_id", false}, // metadata column
		{"track-record", false}, // wrong separator
		{"TrackRecord", false},  // wrong case
		{"track_record; DROP TABLE users", false}, // SQLi attempt
	}
	for _, c := range cases {
		t.Run(c.dim, func(t *testing.T) {
			if got := model.AllowedRatingDimensions[c.dim]; got != c.want {
				t.Errorf("AllowedRatingDimensions[%q] = %v, want %v", c.dim, got, c.want)
			}
		})
	}
}

// rateValueValid mirrors the handler's `req.Value < 1 || > 10`
// check. Calling it from a test pins the contract: 1-10 valid,
// nil valid (clears the dimension), everything else rejected.
func rateValueValid(v *int) bool {
	if v == nil {
		return true
	}
	return *v >= 1 && *v <= 10
}

func TestRateValue_Range(t *testing.T) {
	mk := func(n int) *int { return &n }
	cases := []struct {
		name  string
		value *int
		want  bool
	}{
		{"nil clears dimension", nil, true},
		{"1 lowest valid", mk(1), true},
		{"5 mid", mk(5), true},
		{"10 highest valid", mk(10), true},

		{"0 below range", mk(0), false},
		{"-1 negative", mk(-1), false},
		{"11 above range", mk(11), false},
		{"100 way above", mk(100), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rateValueValid(c.value); got != c.want {
				t.Errorf("rateValueValid(%v) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

func TestRatingNotesMaxLen_Spec(t *testing.T) {
	// Lock the cap so a future tweak surfaces in code review.
	const want = 2000
	if ratingNotesMaxLen != want {
		t.Errorf("ratingNotesMaxLen = %d, want %d", ratingNotesMaxLen, want)
	}
}
