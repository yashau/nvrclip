package dahua

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yashau/nvrclip/internal/nvr"
)

func TestParseFindNext(t *testing.T) {
	text := `found=2
items[0].Channel=0
items[0].EndTime=2026-05-05 11:00:00
items[0].FilePath=/mnt/dvr/a.dav
items[0].StartTime=2026-05-05 10:00:00
items[0].Type=dav
items[0].VideoStream=Main
items[1].Channel=0
items[1].EndTime=2026-05-05 12:00:00
items[1].FilePath=/mnt/dvr/b.dav
items[1].StartTime=2026-05-05 11:00:00
items[1].Type=dav
items[1].VideoStream=Main
`
	found, segments, err := parseFindNext(text)
	if err != nil {
		t.Fatal(err)
	}
	if found != 2 {
		t.Fatalf("found = %d, want 2", found)
	}
	if len(segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(segments))
	}
	if segments[0].FilePath != "/mnt/dvr/a.dav" || segments[1].FilePath != "/mnt/dvr/b.dav" {
		t.Fatalf("unexpected file paths: %#v", segments)
	}
}

func TestPreferMainStream(t *testing.T) {
	segments := []nvr.Segment{
		{Stream: "Extra1", FilePath: "sub.dav"},
		{Stream: "Main", FilePath: "main.dav"},
	}
	got := preferMainStream(segments)
	if len(got) != 1 || got[0].FilePath != "main.dav" {
		t.Fatalf("preferred segments = %#v", got)
	}
}

func TestDownloadUsesIndexedRecordingFile(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write([]byte("original recording"))
	}))
	defer server.Close()

	client := &Client{baseURLs: []string{server.URL}, http: server.Client()}
	start := time.Date(2026, 6, 20, 15, 0, 0, 0, time.Local)
	path := filepath.Join(t.TempDir(), "part.src")
	result, err := client.Download(context.Background(), nvr.DownloadRequest{
		Channel: 1,
		From:    start.Add(10 * time.Minute),
		To:      start.Add(20 * time.Minute),
		Path:    path,
		Segment: nvr.Segment{
			Start:    start,
			End:      start.Add(time.Hour),
			FilePath: "/mnt/dvr/main recording[R][0@0][0].dav",
			Stream:   "Main",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if requestedPath != "/cgi-bin/RPC_Loadfile/mnt/dvr/main recording[R][0@0][0].dav" {
		t.Fatalf("requested path = %q", requestedPath)
	}
	if !result.From.Equal(start) || !result.To.Equal(start.Add(time.Hour)) {
		t.Fatalf("download range = %s - %s", result.From, result.To)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original recording" {
		t.Fatalf("downloaded data = %q", data)
	}
}

func TestDownloadFallsBackToBoundedExport(t *testing.T) {
	var bounded bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/loadfile.cgi" {
			http.NotFound(w, r)
			return
		}
		bounded = true
		if r.URL.Query().Get("subtype") != "0" {
			t.Fatalf("subtype = %q", r.URL.Query().Get("subtype"))
		}
		_, _ = w.Write([]byte("bounded recording"))
	}))
	defer server.Close()

	client := &Client{baseURLs: []string{server.URL}, http: server.Client()}
	start := time.Date(2026, 6, 20, 15, 0, 0, 0, time.Local)
	from := start.Add(10 * time.Minute)
	to := start.Add(20 * time.Minute)
	result, err := client.Download(context.Background(), nvr.DownloadRequest{
		Channel: 1,
		From:    from,
		To:      to,
		Path:    filepath.Join(t.TempDir(), "part.src"),
		Segment: nvr.Segment{
			Start:    start,
			End:      start.Add(time.Hour),
			FilePath: "/mnt/dvr/main.dav",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bounded {
		t.Fatal("bounded fallback was not requested")
	}
	if !result.From.Equal(from) || !result.To.Equal(to) {
		t.Fatalf("download range = %s - %s", result.From, result.To)
	}
}

func TestCurrentTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/global.cgi" || r.URL.Query().Get("action") != "getCurrentTime" {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		_, _ = w.Write([]byte("result=2026-07-26 14:30:15\n"))
	}))
	defer server.Close()

	client := &Client{baseURLs: []string{server.URL}, http: server.Client()}
	current, err := client.CurrentTime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 26, 14, 30, 15, 0, time.Local)
	if !current.Equal(want) {
		t.Fatalf("current time = %s, want %s", current, want)
	}
}
