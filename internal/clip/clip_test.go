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

func TestDetectStalePreambleUsesFirstKeyframeAfterDateJump(t *testing.T) {
	packets := []videoPacket{
		{DTS: 100, Position: 100, Keyframe: true},
		{DTS: 100.04, Position: 200},
		{DTS: 102.92, Position: 300},
		{DTS: 1_050_064, Position: 400},
		{DTS: 1_050_066, Position: 500},
		{DTS: 1_050_069.2, Position: 3_288_213, Keyframe: true},
	}

	got, found, err := detectStalePreamble(packets)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("stale preamble was not detected")
	}
	if got.Bytes != 3_288_213 {
		t.Fatalf("skip bytes = %d, want 3288213", got.Bytes)
	}
	if got.Advance != 5*time.Second+200*time.Millisecond {
		t.Fatalf("keyframe advance = %s, want 5.2s", got.Advance)
	}
	if got.TimestampJump < 290*time.Hour {
		t.Fatalf("timestamp jump = %s, want a multi-day jump", got.TimestampJump)
	}
}

func TestDetectStalePreambleIgnoresOrdinaryPacketGap(t *testing.T) {
	packets := []videoPacket{
		{DTS: 100, Position: 100, Keyframe: true},
		{DTS: 100.04, Position: 200},
		{DTS: 5 * 60, Position: 300},
		{DTS: 5*60 + 2, Position: 400, Keyframe: true},
	}

	_, found, err := detectStalePreamble(packets)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("ordinary timestamp gap was treated as a stale preamble")
	}
}

func TestDetectStalePreambleRequiresNearbyKeyframe(t *testing.T) {
	packets := []videoPacket{
		{DTS: 100, Position: 100, Keyframe: true},
		{DTS: 100.04, Position: 200},
		{DTS: 100_000, Position: 300},
		{DTS: 100_031, Position: 400, Keyframe: true},
	}

	_, found, err := detectStalePreamble(packets)
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("distant keyframe was used as a stale preamble boundary")
	}
}

func TestAdjustTrimForPreamble(t *testing.T) {
	tests := []struct {
		name            string
		offset          time.Duration
		duration        time.Duration
		advance         time.Duration
		wantOffset      time.Duration
		wantDuration    time.Duration
		wantMissingLead time.Duration
	}{
		{
			name:            "request starts after clean keyframe",
			offset:          30 * time.Minute,
			duration:        10 * time.Minute,
			advance:         5*time.Second + 200*time.Millisecond,
			wantOffset:      29*time.Minute + 54*time.Second + 800*time.Millisecond,
			wantDuration:    10 * time.Minute,
			wantMissingLead: 0,
		},
		{
			name:            "request starts before clean keyframe",
			offset:          time.Second,
			duration:        30 * time.Minute,
			advance:         5*time.Second + 200*time.Millisecond,
			wantOffset:      0,
			wantDuration:    29*time.Minute + 55*time.Second + 800*time.Millisecond,
			wantMissingLead: 4*time.Second + 200*time.Millisecond,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotOffset, gotDuration, gotMissingLead := adjustTrimForPreamble(test.offset, test.duration, test.advance)
			if gotOffset != test.wantOffset || gotDuration != test.wantDuration || gotMissingLead != test.wantMissingLead {
				t.Fatalf(
					"adjusted trim = offset %s duration %s missing %s, want offset %s duration %s missing %s",
					gotOffset,
					gotDuration,
					gotMissingLead,
					test.wantOffset,
					test.wantDuration,
					test.wantMissingLead,
				)
			}
		})
	}
}
