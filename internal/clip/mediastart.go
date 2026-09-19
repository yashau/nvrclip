package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	mediaStartProbePacketLimit = 2000
	// Enough consecutive packets to tell a real timeline from a codec that
	// reports one timestamp and then stops.
	minimumUsableTimestampRun = 8
)

type mediaStartProbeOutput struct {
	Packets []struct {
		PTSTime string `json:"pts_time"`
		DTSTime string `json:"dts_time"`
		Pos     string `json:"pos"`
	} `json:"packets"`
}

// probeUsableTimestamps reports whether the recording carries a timeline worth
// seeking against, looking only at the part the trim will keep.
//
// The timestamps are deliberately never compared with the recorder's clock or
// with the time its index claims. Both are unreliable, and neither is needed:
// the trim only ever seeks to a position relative to the start of this file, so
// a timeline that advances sensibly is enough, whatever epoch it is written in.
func probeUsableTimestamps(ctx context.Context, input string, minPosition int64) (bool, error) {
	ffprobe, err := findFFprobe()
	if err != nil {
		return false, err
	}
	args := []string{
		"-v", "error",
		"-f", "dhav",
		"-select_streams", "v:0",
		"-read_intervals", fmt.Sprintf("%%+#%d", mediaStartProbePacketLimit),
		"-show_packets",
		"-show_entries", "packet=pts_time,dts_time,pos",
		"-of", "json",
		input,
	}
	cmd := exec.CommandContext(ctx, ffprobe, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			return false, fmt.Errorf("ffprobe timestamp scan failed: %w: %s", err, msg)
		}
		return false, fmt.Errorf("ffprobe timestamp scan failed: %w", err)
	}

	var output mediaStartProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return false, fmt.Errorf("parse ffprobe timestamp scan: %w", err)
	}
	return hasUsableTimestamps(collectTimestamps(output, minPosition)), nil
}

func collectTimestamps(output mediaStartProbeOutput, minPosition int64) []float64 {
	var stamps []float64
	for _, raw := range output.Packets {
		if position, err := strconv.ParseInt(raw.Pos, 10, 64); err == nil {
			if position < minPosition {
				continue
			}
		}
		value := raw.PTSTime
		if value == "" || value == "N/A" {
			value = raw.DTSTime
		}
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil {
			continue
		}
		stamps = append(stamps, seconds)
	}
	return stamps
}

// hasUsableTimestamps looks for a timeline that moves forward and actually
// advances, which is all a relative seek needs. A run of identical or backward
// timestamps means ffmpeg would have nothing coherent to seek through.
func hasUsableTimestamps(stamps []float64) bool {
	if len(stamps) < minimumUsableTimestampRun {
		return false
	}
	for i := 1; i < len(stamps); i++ {
		if stamps[i] < stamps[i-1] {
			return false
		}
	}
	return stamps[len(stamps)-1] > stamps[0]
}
