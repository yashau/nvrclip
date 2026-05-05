package nvrhttp

import "testing"

func TestParseBaseURLsHostOnly(t *testing.T) {
	got, err := ParseBaseURLs("10.10.37.5")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://10.10.37.5", "http://10.10.37.5"}
	if len(got.URLs) != len(want) {
		t.Fatalf("URLs = %#v", got.URLs)
	}
	for i := range want {
		if got.URLs[i] != want[i] {
			t.Fatalf("URLs[%d] = %q, want %q", i, got.URLs[i], want[i])
		}
	}
}

func TestParseBaseURLsExplicitScheme(t *testing.T) {
	got, err := ParseBaseURLs("https://10.10.37.5/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.URLs) != 1 || got.URLs[0] != "https://10.10.37.5" {
		t.Fatalf("URLs = %#v", got.URLs)
	}
}
