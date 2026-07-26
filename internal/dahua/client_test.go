package dahua

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
