package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	preambleProbePacketLimit  = 2000
	preambleSearchPacketLimit = 512
	preambleKeyPacketLimit    = 1000
	minimumPreambleJump       = 6 * time.Hour
	maximumPreambleMediaSpan  = 2 * time.Minute
	maximumPreambleKeyDelay   = 30 * time.Second
)

type videoPacket struct {
	DTS      float64
	Position int64
	Keyframe bool
}

type preambleSkip struct {
	Bytes         int64
	Advance       time.Duration
	TimestampJump time.Duration
}

type packetProbeOutput struct {
	Packets []struct {
		DTSTime string `json:"dts_time"`
		Flags   string `json:"flags"`
		Pos     string `json:"pos"`
	} `json:"packets"`
}

func probeStalePreamble(ctx context.Context, input string) (preambleSkip, bool, error) {
	ffprobe, err := findFFprobe()
	if err != nil {
		return preambleSkip{}, false, err
	}
	args := []string{
		"-v", "error",
		"-f", "dhav",
		"-select_streams", "v:0",
		"-read_intervals", fmt.Sprintf("%%+#%d", preambleProbePacketLimit),
		"-show_packets",
		"-show_entries", "packet=dts_time,pos,flags",
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
			return preambleSkip{}, false, fmt.Errorf("ffprobe packet scan failed: %w: %s", err, msg)
		}
		return preambleSkip{}, false, fmt.Errorf("ffprobe packet scan failed: %w", err)
	}

	var output packetProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return preambleSkip{}, false, fmt.Errorf("parse ffprobe packet scan: %w", err)
	}
	packets := make([]videoPacket, 0, len(output.Packets))
	for _, raw := range output.Packets {
		dts, err := strconv.ParseFloat(raw.DTSTime, 64)
		if err != nil {
			continue
		}
		position, err := strconv.ParseInt(raw.Pos, 10, 64)
		if err != nil {
			continue
		}
		packets = append(packets, videoPacket{
			DTS:      dts,
			Position: position,
			Keyframe: strings.Contains(raw.Flags, "K"),
		})
	}
	return detectStalePreamble(packets)
}

func detectStalePreamble(packets []videoPacket) (preambleSkip, bool, error) {
	if len(packets) < 2 {
		return preambleSkip{}, false, nil
	}
	searchLimit := min(len(packets), preambleSearchPacketLimit)
	jumpIndex := -1
	largestJump := float64(minimumPreambleJump) / float64(time.Second)
	firstDTS := packets[0].DTS
	maximumSpan := float64(maximumPreambleMediaSpan) / float64(time.Second)
	for i := 1; i < searchLimit; i++ {
		if math.Abs(packets[i-1].DTS-firstDTS) > maximumSpan {
			break
		}
		jump := math.Abs(packets[i].DTS - packets[i-1].DTS)
		if jump >= largestJump {
			largestJump = jump
			jumpIndex = i
		}
	}
	if jumpIndex < 0 {
		return preambleSkip{}, false, nil
	}

	newRunDTS := packets[jumpIndex].DTS
	keyLimit := min(len(packets), jumpIndex+preambleKeyPacketLimit)
	maximumKeyDelay := float64(maximumPreambleKeyDelay) / float64(time.Second)
	for i := jumpIndex; i < keyLimit; i++ {
		keyDelay := packets[i].DTS - newRunDTS
		if keyDelay < 0 {
			continue
		}
		if keyDelay > maximumKeyDelay {
			break
		}
		if !packets[i].Keyframe || packets[i].Position <= 0 {
			continue
		}
		return preambleSkip{
			Bytes:         packets[i].Position,
			Advance:       secondsDuration(keyDelay),
			TimestampJump: secondsDuration(largestJump),
		}, true, nil
	}
	return preambleSkip{}, false, nil
}

func adjustTrimForPreamble(offset time.Duration, duration time.Duration, advance time.Duration) (time.Duration, time.Duration, time.Duration) {
	if advance <= 0 {
		return offset, duration, 0
	}
	if offset >= advance {
		return offset - advance, duration, 0
	}
	missingLead := advance - offset
	if missingLead >= duration {
		return 0, 0, missingLead
	}
	return 0, duration - missingLead, missingLead
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(math.Round(seconds * float64(time.Second)))
}
