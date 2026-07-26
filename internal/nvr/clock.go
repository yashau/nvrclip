package nvr

import (
	"context"
	"time"
)

// Clock is implemented by adapters that can read the recorder's current time.
type Clock interface {
	CurrentTime(ctx context.Context) (time.Time, error)
}

type ClockSample struct {
	NVRTime time.Time
	PCTime  time.Time
	Offset  time.Duration
}

// MeasureClockOffset compares the recorder's wall clock with the midpoint of
// the local request. The offset is NVR time minus PC time.
func MeasureClockOffset(ctx context.Context, clock Clock, now func() time.Time) (ClockSample, error) {
	before := now()
	nvrTime, err := clock.CurrentTime(ctx)
	if err != nil {
		return ClockSample{}, err
	}
	after := now()
	pcTime := before.Add(after.Sub(before) / 2)
	offset := nvrTime.Sub(pcTime).Round(time.Second)
	return ClockSample{
		NVRTime: nvrTime,
		PCTime:  pcTime,
		Offset:  offset,
	}, nil
}

// WithClockOffset translates user/PC times to NVR times at the adapter
// boundary, then translates returned timestamps back to the user's clock.
func WithClockOffset(adapter Adapter, offset time.Duration) Adapter {
	return &clockAdjustedAdapter{adapter: adapter, offset: offset}
}

type clockAdjustedAdapter struct {
	adapter Adapter
	offset  time.Duration
}

func (a *clockAdjustedAdapter) Search(ctx context.Context, channel int, from time.Time, to time.Time) ([]Segment, error) {
	segments, err := a.adapter.Search(ctx, channel, from.Add(a.offset), to.Add(a.offset))
	if err != nil {
		return nil, err
	}
	for i := range segments {
		if !segments[i].Start.IsZero() {
			segments[i].Start = segments[i].Start.Add(-a.offset)
		}
		if !segments[i].End.IsZero() {
			segments[i].End = segments[i].End.Add(-a.offset)
		}
	}
	return segments, nil
}

func (a *clockAdjustedAdapter) Download(ctx context.Context, req DownloadRequest) (DownloadResult, error) {
	req.From = req.From.Add(a.offset)
	req.To = req.To.Add(a.offset)
	if !req.Segment.Start.IsZero() {
		req.Segment.Start = req.Segment.Start.Add(a.offset)
	}
	if !req.Segment.End.IsZero() {
		req.Segment.End = req.Segment.End.Add(a.offset)
	}

	result, err := a.adapter.Download(ctx, req)
	if err != nil {
		return DownloadResult{}, err
	}
	if !result.From.IsZero() {
		result.From = result.From.Add(-a.offset)
	}
	if !result.To.IsZero() {
		result.To = result.To.Add(-a.offset)
	}
	return result, nil
}
