package nvr

import (
	"context"
	"time"
)

type Segment struct {
	Channel  int
	Start    time.Time
	End      time.Time
	FilePath string
	Type     string
	Stream   string
}

type DownloadRequest struct {
	Channel  int
	From     time.Time
	To       time.Time
	Path     string
	Segment  Segment
	Progress func(Progress)
	Logf     func(string, ...any)
}

type DownloadResult struct {
	From                 time.Time
	To                   time.Time
	ForceFrameRate       bool
	DiscardStalePreamble bool
}

type Progress struct {
	Downloaded int64
	Total      int64
}

type Adapter interface {
	Search(ctx context.Context, channel int, from time.Time, to time.Time) ([]Segment, error)
	Download(ctx context.Context, req DownloadRequest) (DownloadResult, error)
}
