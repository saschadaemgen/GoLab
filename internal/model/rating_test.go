package model

import (
	"math"
	"testing"
)

// p is a tiny helper so the test cases below can declare 1-10
// rating values without seven lines of `v := 8; r.TrackRecord = &v`.
func p(n int) *int { return &n }

func TestApplicationRating_AverageEmpty(t *testing.T) {
	r := &ApplicationRating{UserID: 42}
	if got := r.Average(); got != 0 {
		t.Errorf("empty rating Average = %v, want 0", got)
	}
	if got := r.RatedCount(); got != 0 {
		t.Errorf("empty rating RatedCount = %v, want 0", got)
	}
}

func TestApplicationRating_AverageNilReceiver(t *testing.T) {
	// The admin pending-users panel hands the rating pointer to the
	// template; if a join misses for some user, we must not panic on
	// a nil rating.
	var r *ApplicationRating
	if got := r.Average(); got != 0 {
		t.Errorf("nil receiver Average = %v, want 0", got)
	}
	if got := r.RatedCount(); got != 0 {
		t.Errorf("nil receiver RatedCount = %v, want 0", got)
	}
}

func TestApplicationRating_AverageOne(t *testing.T) {
	r := &ApplicationRating{UserID: 1, TrackRecord: p(8)}
	if got := r.Average(); got != 8 {
		t.Errorf("single dimension Average = %v, want 8", got)
	}
	if got := r.RatedCount(); got != 1 {
		t.Errorf("single dimension RatedCount = %v, want 1", got)
	}
}

func TestApplicationRating_AverageMultiple(t *testing.T) {
	// 7 + 9 + 5 = 21, /3 = 7.0
	r := &ApplicationRating{
		UserID:                1,
		TrackRecord:           p(7),
		ContributionPotential: p(9),
		Communication:         p(5),
	}
	if got := r.Average(); got != 7.0 {
		t.Errorf("three-dim Average = %v, want 7.0", got)
	}
	if got := r.RatedCount(); got != 3 {
		t.Errorf("three-dim RatedCount = %v, want 3", got)
	}
}

func TestApplicationRating_AverageRoundsCorrectly(t *testing.T) {
	// 7 + 8 = 15, /2 = 7.5 - fractional avg verifies the float
	// division path. No rounding applied at the model level; the
	// admin template formats with .toFixed(1) on the JS side.
	r := &ApplicationRating{
		UserID:       1,
		EcosystemFit: p(7),
		Relevance:    p(8),
	}
	got := r.Average()
	if math.Abs(got-7.5) > 1e-9 {
		t.Errorf("two-dim Average = %v, want 7.5", got)
	}
}

func TestApplicationRating_AverageAllFive(t *testing.T) {
	// All five dimensions at 10: average must be exactly 10.
	r := &ApplicationRating{
		UserID:                1,
		TrackRecord:           p(10),
		EcosystemFit:          p(10),
		ContributionPotential: p(10),
		Relevance:             p(10),
		Communication:         p(10),
	}
	if got := r.Average(); got != 10.0 {
		t.Errorf("max-rated Average = %v, want 10.0", got)
	}
	if got := r.RatedCount(); got != 5 {
		t.Errorf("max-rated RatedCount = %v, want 5", got)
	}
}

func TestApplicationRating_AverageMixedNilAndZeroProtection(t *testing.T) {
	// Sanity: NULL dimensions are skipped, not counted as 0. A
	// rating with only TrackRecord=4 should average to 4, not 0.8.
	r := &ApplicationRating{UserID: 1, TrackRecord: p(4)}
	if got := r.Average(); got != 4.0 {
		t.Errorf("nil-skip Average = %v, want 4.0 (not 0.8)", got)
	}
}

func TestAllowedRatingDimensions_Spec(t *testing.T) {
	// Lock the dimension allow-list against the migration column
	// names. Adding a column requires touching this set; this test
	// fails loudly if the model and the migration drift apart.
	want := []string{
		"track_record",
		"ecosystem_fit",
		"contribution_potential",
		"relevance",
		"communication",
	}
	if len(AllowedRatingDimensions) != len(want) {
		t.Errorf("AllowedRatingDimensions has %d entries, want %d",
			len(AllowedRatingDimensions), len(want))
	}
	for _, name := range want {
		if !AllowedRatingDimensions[name] {
			t.Errorf("AllowedRatingDimensions missing %q", name)
		}
	}
}
