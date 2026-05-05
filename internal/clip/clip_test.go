package clip

import (
	"testing"
	"time"

	"github.com/yashau/nvrclip/internal/nvr"
)

func TestOverlappingSegments(t *testing.T) {
	base := time.Date(2026, 5, 5, 14, 0, 0, 0, time.Local)
	segments := []nvr.Segment{
		{Start: base.Add(-time.Hour), End: base.Add(5 * time.Minute)},
		{Start: base.Add(5 * time.Minute), End: base.Add(30 * time.Minute)},
	}
	got := overlappingSegments(segments, base, base.Add(10*time.Minute))
	if len(got) != 2 {
		t.Fatalf("overlaps = %d, want 2", len(got))
	}
	if !got[0].From.Equal(base) || !got[0].To.Equal(base.Add(5*time.Minute)) {
		t.Fatalf("first overlap = %s - %s", got[0].From, got[0].To)
	}
	if !got[1].From.Equal(base.Add(5*time.Minute)) || !got[1].To.Equal(base.Add(10*time.Minute)) {
		t.Fatalf("second overlap = %s - %s", got[1].From, got[1].To)
	}
}
