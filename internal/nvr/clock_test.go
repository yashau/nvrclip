package nvr

import (
	"context"
	"testing"
	"time"
)

func TestMeasureClockOffsetUsesRequestMidpoint(t *testing.T) {
	local := time.FixedZone("local", 5*60*60)
	pcStart := time.Date(2026, 7, 26, 14, 0, 0, 0, local)
	times := []time.Time{pcStart, pcStart.Add(2 * time.Second)}
	index := 0
	now := func() time.Time {
		value := times[index]
		index++
		return value
	}
	clock := fakeClock{current: pcStart.Add(-time.Hour).Add(time.Second)}

	sample, err := MeasureClockOffset(context.Background(), clock, now)
	if err != nil {
		t.Fatal(err)
	}
	if sample.Offset != -time.Hour {
		t.Fatalf("offset = %s, want -1h", sample.Offset)
	}
	if !sample.PCTime.Equal(pcStart.Add(time.Second)) {
		t.Fatalf("PC midpoint = %s", sample.PCTime)
	}
}

func TestClockAdjustedAdapterTranslatesBothDirections(t *testing.T) {
	base := time.Date(2026, 7, 25, 23, 0, 0, 0, time.Local)
	inner := &recordingAdapter{}
	adapter := WithClockOffset(inner, -time.Hour)

	segments, err := adapter.Search(context.Background(), 3, base, base.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !inner.searchFrom.Equal(base.Add(-time.Hour)) || !inner.searchTo.Equal(base.Add(-30*time.Minute)) {
		t.Fatalf("inner search = %s - %s", inner.searchFrom, inner.searchTo)
	}
	if !segments[0].Start.Equal(base) || !segments[0].End.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("normalized segment = %s - %s", segments[0].Start, segments[0].End)
	}

	result, err := adapter.Download(context.Background(), DownloadRequest{
		From:    base,
		To:      base.Add(30 * time.Minute),
		Segment: segments[0],
	})
	if err != nil {
		t.Fatal(err)
	}
	if !inner.downloadRequest.From.Equal(base.Add(-time.Hour)) {
		t.Fatalf("inner download from = %s", inner.downloadRequest.From)
	}
	if !inner.downloadRequest.Segment.Start.Equal(base.Add(-time.Hour)) {
		t.Fatalf("inner segment start = %s", inner.downloadRequest.Segment.Start)
	}
	if !result.From.Equal(base) || !result.To.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("normalized result = %s - %s", result.From, result.To)
	}
}

func TestClockAdjustedAdapterCrossesDateBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		requested  time.Time
		offset     time.Duration
		wantSearch time.Time
	}{
		{
			name:       "previous day",
			requested:  time.Date(2026, 7, 26, 0, 15, 0, 0, time.Local),
			offset:     -time.Hour,
			wantSearch: time.Date(2026, 7, 25, 23, 15, 0, 0, time.Local),
		},
		{
			name:       "next year",
			requested:  time.Date(2026, 12, 31, 23, 30, 0, 0, time.Local),
			offset:     time.Hour,
			wantSearch: time.Date(2027, 1, 1, 0, 30, 0, 0, time.Local),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inner := &recordingAdapter{}
			adapter := WithClockOffset(inner, test.offset)
			_, err := adapter.Search(
				context.Background(),
				1,
				test.requested,
				test.requested.Add(10*time.Minute),
			)
			if err != nil {
				t.Fatal(err)
			}
			if !inner.searchFrom.Equal(test.wantSearch) {
				t.Fatalf("inner search from = %s, want %s", inner.searchFrom, test.wantSearch)
			}
		})
	}
}

type fakeClock struct {
	current time.Time
}

func (c fakeClock) CurrentTime(context.Context) (time.Time, error) {
	return c.current, nil
}

type recordingAdapter struct {
	searchFrom      time.Time
	searchTo        time.Time
	downloadRequest DownloadRequest
}

func (a *recordingAdapter) Search(_ context.Context, _ int, from time.Time, to time.Time) ([]Segment, error) {
	a.searchFrom = from
	a.searchTo = to
	return []Segment{{Start: from, End: to, FilePath: "recording"}}, nil
}

func (a *recordingAdapter) Download(_ context.Context, req DownloadRequest) (DownloadResult, error) {
	a.downloadRequest = req
	return DownloadResult{From: req.From, To: req.To}, nil
}
