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
	Progress func(Progress)
}

type Progress struct {
	Downloaded int64
	Total      int64
}

type Adapter interface {
	Search(ctx context.Context, channel int, from time.Time, to time.Time) ([]Segment, error)
	Download(ctx context.Context, req DownloadRequest) error
}
