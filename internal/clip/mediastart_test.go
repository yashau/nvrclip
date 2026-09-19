package clip

import (
	"testing"
	"time"
)

func TestHasUsableTimestamps(t *testing.T) {
	run := func(start float64, step float64, count int) []float64 {
		stamps := make([]float64, count)
		for i := range stamps {
			stamps[i] = start + float64(i)*step
		}
		return stamps
	}

	tests := []struct {
		name   string
		stamps []float64
		want   bool
	}{
		{
			// Recorder epochs are accepted whatever they read, because the trim
			// only ever seeks relative to the start of the file.
			name:   "recorder wall-clock epoch",
			stamps: run(1789758007.881, 0.04, 40),
			want:   true,
		},
		{
			name:   "timeline starting at zero",
			stamps: run(0, 0.04, 40),
			want:   true,
		},
		{
			// A gap is exactly what must survive: collapsing it is the bug this
			// replaced.
			name:   "timeline with a recording gap",
			stamps: append(run(100, 0.04, 20), run(140, 0.04, 20)...),
			want:   true,
		},
		{
			name:   "too few packets to judge",
			stamps: run(100, 0.04, 3),
			want:   false,
		},
		{
			name:   "timestamps never advance",
			stamps: run(100, 0, 40),
			want:   false,
		},
		{
			name:   "timestamps go backwards",
			stamps: append(run(100, 0.04, 20), 50),
			want:   false,
		},
		{
			name:   "no timestamps at all",
			stamps: nil,
			want:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasUsableTimestamps(test.stamps); got != test.want {
				t.Fatalf("hasUsableTimestamps = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCollectTimestampsSkipsDiscardedPreamble(t *testing.T) {
	var output mediaStartProbeOutput
	output.Packets = []struct {
		PTSTime string `json:"pts_time"`
		DTSTime string `json:"dts_time"`
		Pos     string `json:"pos"`
	}{
		{PTSTime: "1788708088.000", Pos: "1006904"},
		{PTSTime: "1789758007.881", Pos: "3232206"},
		{PTSTime: "1789758008.000", Pos: "3519579"},
		{PTSTime: "N/A", DTSTime: "1789758008.080", Pos: "3545259"},
	}

	got := collectTimestamps(output, 3232206)
	want := []float64{1789758007.881, 1789758008.000, 1789758008.080}
	if len(got) != len(want) {
		t.Fatalf("collectTimestamps returned %d stamps, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stamp %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestOutputNameKeepsSecondsWhenRangeIsNotWholeMinutes(t *testing.T) {
	tests := []struct {
		name string
		from time.Time
		to   time.Time
		want string
	}{
		{
			name: "whole minutes stay short",
			from: time.Date(2026, 9, 6, 14, 0, 0, 0, time.Local),
			to:   time.Date(2026, 9, 6, 14, 10, 0, 0, time.Local),
			want: "shop_cashier_2026-09-06_1400-1410.mp4",
		},
		{
			name: "odd --minutes span keeps its seconds",
			from: time.Date(2026, 9, 6, 14, 0, 30, 0, time.Local),
			to:   time.Date(2026, 9, 6, 14, 9, 30, 0, time.Local),
			want: "shop_cashier_2026-09-06_140030-140930.mp4",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := outputName("shop cashier", test.from, test.to)
			if got != test.want {
				t.Fatalf("outputName = %q, want %q", got, test.want)
			}
		})
	}
}
