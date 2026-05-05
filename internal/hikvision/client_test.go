package hikvision

import (
	"strings"
	"testing"
	"time"
)

func TestParseHikTimeKeepsWallClockForZ(t *testing.T) {
	got, err := parseHikTime("2026-05-05T12:20:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hour() != 12 || got.Minute() != 20 {
		t.Fatalf("time = %s, want local wall-clock 12:20", got)
	}
}

func TestDownloadRequestEscapesPlaybackURI(t *testing.T) {
	xml := downloadRequest("rtsp://10.10.37.5/Streaming/tracks/101/?starttime=20260505T122000Z&endtime=20260505T122010Z")
	if !strings.Contains(xml, "&amp;endtime=") {
		t.Fatalf("download request did not XML-escape URI: %s", xml)
	}
}

func TestTrackID(t *testing.T) {
	if got := trackID(12); got != 1201 {
		t.Fatalf("trackID(12) = %d, want 1201", got)
	}
}

func TestHikSearchTime(t *testing.T) {
	tm := time.Date(2026, 5, 5, 12, 20, 0, 0, time.FixedZone("UTC+5", 5*60*60))
	if got := hikSearchTime(tm); got != "2026-05-05T12:20:00+05:00" {
		t.Fatalf("hikSearchTime = %q", got)
	}
}
