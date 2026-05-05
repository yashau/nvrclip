package clip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/yashau/nvrclip/internal/nvr"
)

type Mode string

const (
	ModeCopy  Mode = "copy"
	ModeExact Mode = "exact"
)

type Job struct {
	Adapter      nvr.Adapter
	Alias        string
	Channel      int
	From         time.Time
	To           time.Time
	OutputDir    string
	WorkDir      string
	KeepTemp     bool
	DownloadOnly bool
	Mode         Mode
	FrameRate    float64
	Stdout       io.Writer
}

type Result struct {
	OutputPath string
	WorkDir    string
}

type overlap struct {
	Segment nvr.Segment
	From    time.Time
	To      time.Time
}

func Run(ctx context.Context, job Job) (Result, error) {
	if job.Adapter == nil {
		return Result{}, errors.New("adapter is nil")
	}
	if job.Mode == "" {
		job.Mode = ModeCopy
	}
	if job.Mode != ModeCopy && job.Mode != ModeExact {
		return Result{}, fmt.Errorf("unsupported mode %q", job.Mode)
	}
	if job.FrameRate <= 0 {
		job.FrameRate = 25
	}
	if job.OutputDir == "" {
		job.OutputDir = "."
	}
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		return Result{}, err
	}

	workDir := job.WorkDir
	var cleanup bool
	if workDir == "" {
		tmp, err := os.MkdirTemp("", "nvrclip-*")
		if err != nil {
			return Result{}, err
		}
		workDir = tmp
		cleanup = !job.KeepTemp && !job.DownloadOnly
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Result{}, err
	}
	if cleanup {
		defer os.RemoveAll(workDir)
	}

	segments, err := job.Adapter.Search(ctx, job.Channel, job.From, job.To)
	if err != nil {
		return Result{}, err
	}
	overlaps := overlappingSegments(segments, job.From, job.To)
	if len(overlaps) == 0 {
		return Result{}, fmt.Errorf("no recordings overlap %s to %s on channel %d", humanTime(job.From), humanTime(job.To), job.Channel)
	}

	parts := make([]string, 0, len(overlaps))
	for i, ov := range overlaps {
		part := filepath.Join(workDir, fmt.Sprintf("part_%03d.dav", i))
		progress := newPartProgress(job.Stdout, i+1, len(overlaps), ov.From, ov.To)
		progress.Start()
		if err := job.Adapter.Download(ctx, nvr.DownloadRequest{
			Channel:  job.Channel,
			From:     ov.From,
			To:       ov.To,
			Path:     part,
			Progress: progress.Update,
		}); err != nil {
			progress.Done(0)
			return Result{WorkDir: workDir}, err
		}
		info, err := os.Stat(part)
		if err != nil {
			progress.Done(0)
			return Result{WorkDir: workDir}, err
		}
		progress.Done(info.Size())
		if info.Size() == 0 {
			return Result{WorkDir: workDir}, fmt.Errorf("downloaded empty part for %s", humanRange(ov.From, ov.To))
		}
		parts = append(parts, part)
	}

	joined := filepath.Join(workDir, "joined.dav")
	if err := joinFiles(joined, parts); err != nil {
		return Result{WorkDir: workDir}, err
	}
	if job.DownloadOnly {
		return Result{WorkDir: workDir}, nil
	}

	outPath := filepath.Join(job.OutputDir, outputName(job.Alias, job.From, job.To))
	if err := renderMP4(ctx, joined, outPath, job.FrameRate, job.To.Sub(job.From), job.Mode); err != nil {
		return Result{WorkDir: workDir}, err
	}
	return Result{OutputPath: outPath, WorkDir: workDir}, nil
}

func overlappingSegments(segments []nvr.Segment, from time.Time, to time.Time) []overlap {
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Start.Before(segments[j].Start)
	})
	var out []overlap
	for _, seg := range segments {
		start := maxTime(from, seg.Start)
		end := minTime(to, seg.End)
		if end.After(start) {
			out = append(out, overlap{Segment: seg, From: start, To: end})
		}
	}
	return out
}

func joinFiles(dst string, parts []string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, part := range parts {
		in, err := os.Open(part)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			return err
		}
		if err := in.Close(); err != nil {
			return err
		}
	}
	return nil
}

func renderMP4(ctx context.Context, input string, output string, fps float64, duration time.Duration, mode Mode) error {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return err
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-fflags", "+genpts",
		"-r", fmt.Sprintf("%.3f", fps),
		"-i", input,
		"-map", "0:v:0",
		"-an",
		"-t", fmt.Sprintf("%.3f", duration.Seconds()),
	}
	switch mode {
	case ModeCopy:
		args = append(args, "-c:v", "copy")
	case ModeExact:
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-pix_fmt", "yuv420p")
	default:
		return fmt.Errorf("unsupported mode %q", mode)
	}
	args = append(args, "-movflags", "+faststart", output)

	cmd := exec.CommandContext(ctx, ffmpeg, args...)
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
			return fmt.Errorf("ffmpeg failed: %w: %s", err, msg)
		}
		return fmt.Errorf("ffmpeg failed: %w", err)
	}
	return nil
}

func findFFmpeg() (string, error) {
	if override := os.Getenv("NVRCLIP_FFMPEG"); override != "" {
		return override, nil
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.New("ffmpeg not found; install ffmpeg or set NVRCLIP_FFMPEG")
	}
	return path, nil
}

func outputName(alias string, from time.Time, to time.Time) string {
	return fmt.Sprintf("%s_%s-%s.mp4",
		slug(alias),
		from.Format("2006-01-02_1504"),
		to.Format("1504"),
	)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "clip"
	}
	return s
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func humanTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

func humanRange(from time.Time, to time.Time) string {
	return humanTime(from) + " - " + humanTime(to)
}

func logf(w io.Writer, format string, args ...any) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, format, args...)
}

type partProgress struct {
	w       io.Writer
	part    int
	total   int
	from    time.Time
	to      time.Time
	last    time.Time
	enabled bool
}

func newPartProgress(w io.Writer, part int, total int, from time.Time, to time.Time) *partProgress {
	return &partProgress{w: w, part: part, total: total, from: from, to: to, enabled: w != nil}
}

func (p *partProgress) Start() {
	if !p.enabled {
		return
	}
	fmt.Fprintf(p.w, "part %d/%d downloading %s\n", p.part, p.total, humanRange(p.from, p.to))
}

func (p *partProgress) Update(progress nvr.Progress) {
	if !p.enabled {
		return
	}
	if progress.Total > 0 && progress.Downloaded >= progress.Total {
		return
	}
	now := time.Now()
	if !p.last.IsZero() && now.Sub(p.last) < 500*time.Millisecond {
		return
	}
	p.last = now
	if progress.Total > 0 {
		percent := float64(progress.Downloaded) / float64(progress.Total) * 100
		fmt.Fprintf(p.w, "part %d/%d %.1f%% (%s/%s)\r", p.part, p.total, percent, formatBytes(progress.Downloaded), formatBytes(progress.Total))
		return
	}
	fmt.Fprintf(p.w, "part %d/%d %s downloaded\r", p.part, p.total, formatBytes(progress.Downloaded))
}

func (p *partProgress) Done(size int64) {
	if !p.enabled {
		return
	}
	if size > 0 {
		fmt.Fprintf(p.w, "part %d/%d done (%s)                    \n", p.part, p.total, formatBytes(size))
		return
	}
	fmt.Fprintf(p.w, "part %d/%d stopped                    \n", p.part, p.total)
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}
