package dahua

import "testing"

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
