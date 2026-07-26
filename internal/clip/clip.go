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
	"strconv"
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
	LogPath    string
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
	userWorkDir := workDir != ""
	if workDir == "" {
		tmpRoot := filepath.Join(".", "nvrclip_temp")
		if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
			return Result{}, err
		}
		tmp, err := os.MkdirTemp(tmpRoot, "run-*")
		if err != nil {
			return Result{}, err
		}
		workDir = tmp
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Result{}, err
	}
	logger, err := newLogger(filepath.Join(workDir, "nvrclip.log"))
	if err != nil {
		return Result{WorkDir: workDir}, err
	}
	defer logger.Close()
	log := logger.Printf
	log("start alias=%q channel=%d from=%s to=%s output_dir=%q work_dir=%q mode=%s download_only=%t keep_temp=%t",
		job.Alias, job.Channel, humanTime(job.From), humanTime(job.To), job.OutputDir, workDir, job.Mode, job.DownloadOnly, job.KeepTemp)
	if job.Stdout != nil {
		fmt.Fprintf(job.Stdout, "log %s\n", logger.Path)
	}

	log("search start")
	segments, err := job.Adapter.Search(ctx, job.Channel, job.From, job.To)
	if err != nil {
		log("search error: %v", err)
		return Result{WorkDir: workDir, LogPath: logger.Path}, err
	}
	log("search done segments=%d", len(segments))
	for i, seg := range segments {
		log("segment %d channel=%d start=%s end=%s type=%q stream=%q path=%q", i, seg.Channel, humanTime(seg.Start), humanTime(seg.End), seg.Type, seg.Stream, seg.FilePath)
	}
	overlaps := overlappingSegments(segments, job.From, job.To)
	if len(overlaps) == 0 {
		err := fmt.Errorf("no recordings overlap %s to %s on channel %d", humanTime(job.From), humanTime(job.To), job.Channel)
		log("overlap error: %v", err)
		return Result{WorkDir: workDir, LogPath: logger.Path}, err
	}
	log("overlaps=%d", len(overlaps))

	trimmedParts := make([]string, 0, len(overlaps))
	exactWidth := 0
	exactHeight := 0
	for i, ov := range overlaps {
		rawPart := filepath.Join(workDir, fmt.Sprintf("part_%03d.src", i))
		log("part %d/%d download start overlap=%s raw=%q segment_start=%s segment_end=%s", i+1, len(overlaps), humanRange(ov.From, ov.To), rawPart, humanTime(ov.Segment.Start), humanTime(ov.Segment.End))
		progress := newPartProgress(job.Stdout, i+1, len(overlaps), ov.From, ov.To)
		progress.Start()
		download, err := job.Adapter.Download(ctx, nvr.DownloadRequest{
			Channel:  job.Channel,
			From:     ov.From,
			To:       ov.To,
			Path:     rawPart,
			Segment:  ov.Segment,
			Progress: progress.Update,
			Logf:     log,
		})
		if err != nil {
			progress.Done(0)
			log("part %d/%d download error: %v", i+1, len(overlaps), err)
			return Result{WorkDir: workDir, LogPath: logger.Path}, err
		}
		info, err := os.Stat(rawPart)
		if err != nil {
			progress.Done(0)
			log("part %d/%d stat error: %v", i+1, len(overlaps), err)
			return Result{WorkDir: workDir, LogPath: logger.Path}, err
		}
		progress.Done(info.Size())
		log("part %d/%d download done size=%d download_from=%s download_to=%s force_fps=%t", i+1, len(overlaps), info.Size(), humanTime(download.From), humanTime(download.To), download.ForceFrameRate)
		if info.Size() == 0 {
			err := fmt.Errorf("downloaded empty part for %s", humanRange(ov.From, ov.To))
			log("part %d/%d empty error: %v", i+1, len(overlaps), err)
			return Result{WorkDir: workDir, LogPath: logger.Path}, err
		}
		if job.DownloadOnly {
			continue
		}

		trimmedPart := filepath.Join(workDir, fmt.Sprintf("trimmed_%03d.mp4", i))
		offset := ov.From.Sub(download.From)
		if offset < 0 {
			offset = 0
		}
		duration := ov.To.Sub(ov.From)
		var skipInitialBytes int64
		if download.DiscardStalePreamble {
			skip, found, probeErr := probeStalePreamble(ctx, rawPart)
			switch {
			case probeErr != nil:
				log("part %d/%d stale preamble probe failed, continuing without byte skip: %v", i+1, len(overlaps), probeErr)
			case found && skip.Bytes >= info.Size():
				log("part %d/%d stale preamble byte skip ignored because position=%d size=%d", i+1, len(overlaps), skip.Bytes, info.Size())
			case found:
				skipInitialBytes = skip.Bytes
				adjustedOffset, adjustedDuration, missingLead := adjustTrimForPreamble(offset, duration, skip.Advance)
				log("part %d/%d stale preamble discarded bytes=%d timestamp_jump=%s keyframe_advance=%s offset=%s adjusted_offset=%s duration=%s adjusted_duration=%s missing_lead=%s",
					i+1, len(overlaps), skip.Bytes, skip.TimestampJump, skip.Advance, offset, adjustedOffset, duration, adjustedDuration, missingLead)
				offset = adjustedOffset
				duration = adjustedDuration
			default:
				log("part %d/%d stale preamble not detected", i+1, len(overlaps))
			}
		}
		if duration <= 0 {
			err := fmt.Errorf("no decodable video remains for %s after discarding the stale recording preamble", humanRange(ov.From, ov.To))
			log("part %d/%d trim range error: %v", i+1, len(overlaps), err)
			return Result{WorkDir: workDir, LogPath: logger.Path}, err
		}
		if job.Mode == ModeExact && (exactWidth == 0 || exactHeight == 0) {
			width, height, err := probeVideoDimensions(ctx, rawPart, skipInitialBytes, log)
			if err != nil {
				log("exact normalize probe failed, continuing without fixed dimensions: %v", err)
			} else {
				exactWidth = evenDimension(width)
				exactHeight = evenDimension(height)
				log("exact normalize target width=%d height=%d", exactWidth, exactHeight)
			}
		}
		log("part %d/%d trim start raw=%q trimmed=%q offset=%s duration=%s", i+1, len(overlaps), rawPart, trimmedPart, offset, duration)
		if err := renderPartMP4(ctx, rawPart, trimmedPart, renderOptions{
			Offset:           offset,
			Duration:         duration,
			FrameRate:        job.FrameRate,
			Mode:             job.Mode,
			ForceFrameRate:   download.ForceFrameRate,
			SkipInitialBytes: skipInitialBytes,
			TargetWidth:      exactWidth,
			TargetHeight:     exactHeight,
			Logf:             log,
		}); err != nil {
			log("part %d/%d trim error: %v", i+1, len(overlaps), err)
			return Result{WorkDir: workDir, LogPath: logger.Path}, err
		}
		if info, err := os.Stat(trimmedPart); err == nil {
			log("part %d/%d trim done size=%d", i+1, len(overlaps), info.Size())
		}
		trimmedParts = append(trimmedParts, trimmedPart)
	}

	if job.DownloadOnly {
		log("download-only done")
		return Result{WorkDir: workDir, LogPath: logger.Path}, nil
	}

	outPath := filepath.Join(job.OutputDir, outputName(job.Alias, job.From, job.To))
	log("concat start parts=%d output=%q", len(trimmedParts), outPath)
	if err := concatMP4(ctx, trimmedParts, outPath, log); err != nil {
		log("concat error: %v", err)
		return Result{WorkDir: workDir, LogPath: logger.Path}, err
	}
	log("concat done output=%q", outPath)
	if !job.KeepTemp && !job.DownloadOnly && !userWorkDir {
		log("cleanup work_dir=%q", workDir)
		logger.Close()
		_ = os.RemoveAll(workDir)
		return Result{OutputPath: outPath, WorkDir: workDir, LogPath: logger.Path}, nil
	}
	log("done")
	return Result{OutputPath: outPath, WorkDir: workDir, LogPath: logger.Path}, nil
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

type renderOptions struct {
	Offset           time.Duration
	Duration         time.Duration
	FrameRate        float64
	Mode             Mode
	ForceFrameRate   bool
	SkipInitialBytes int64
	Logf             func(string, ...any)
	InputFormat      string
	TargetWidth      int
	TargetHeight     int
}

func renderPartMP4(ctx context.Context, input string, output string, opts renderOptions) error {
	if err := renderPartMP4Once(ctx, input, output, opts); err == nil {
		return nil
	} else {
		if opts.Logf != nil {
			opts.Logf("trim normal input failed, retrying as dhav: %v", err)
		}
		opts.InputFormat = "dhav"
		if retryErr := renderPartMP4Once(ctx, input, output, opts); retryErr != nil {
			return fmt.Errorf("%w; dhav retry failed: %v", err, retryErr)
		}
		return nil
	}
}

func renderPartMP4Once(ctx context.Context, input string, output string, opts renderOptions) error {
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return err
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
	}
	if opts.SkipInitialBytes > 0 {
		args = append(args, "-skip_initial_bytes", strconv.FormatInt(opts.SkipInitialBytes, 10))
	}
	if opts.ForceFrameRate {
		args = append(args,
			"-fflags", "+genpts",
			"-r", fmt.Sprintf("%.3f", opts.FrameRate),
		)
	}
	if opts.InputFormat != "" {
		args = append(args, "-f", opts.InputFormat)
	}
	args = append(args,
		"-i", input,
	)
	if opts.Offset > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", opts.Offset.Seconds()))
	}
	args = append(args,
		"-map", "0:v:0",
		"-an",
		"-t", fmt.Sprintf("%.3f", opts.Duration.Seconds()),
	)
	switch opts.Mode {
	case ModeCopy:
		args = append(args, "-c:v", "copy")
	case ModeExact:
		if opts.TargetWidth > 0 && opts.TargetHeight > 0 {
			filter := fmt.Sprintf(
				"scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1",
				opts.TargetWidth,
				opts.TargetHeight,
				opts.TargetWidth,
				opts.TargetHeight,
			)
			args = append(args, "-vf", filter)
		}
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "18", "-pix_fmt", "yuv420p")
	default:
		return fmt.Errorf("unsupported mode %q", opts.Mode)
	}
	args = append(args, "-movflags", "+faststart", output)
	return runFFmpeg(ctx, ffmpeg, args, opts.Logf)
}

func probeVideoDimensions(ctx context.Context, input string, skipInitialBytes int64, logf func(string, ...any)) (int, int, error) {
	width, height, err := probeVideoDimensionsOnce(ctx, input, "", skipInitialBytes)
	if err == nil {
		return width, height, nil
	}
	if logf != nil {
		logf("ffprobe normal input failed, retrying as dhav: %v", err)
	}
	width, height, retryErr := probeVideoDimensionsOnce(ctx, input, "dhav", skipInitialBytes)
	if retryErr != nil {
		return 0, 0, fmt.Errorf("%w; dhav retry failed: %v", err, retryErr)
	}
	return width, height, nil
}

func probeVideoDimensionsOnce(ctx context.Context, input string, inputFormat string, skipInitialBytes int64) (int, int, error) {
	ffprobe, err := findFFprobe()
	if err != nil {
		return 0, 0, err
	}
	args := []string{"-v", "error"}
	if skipInitialBytes > 0 {
		args = append(args, "-skip_initial_bytes", strconv.FormatInt(skipInitialBytes, 10))
	}
	if inputFormat != "" {
		args = append(args, "-f", inputFormat)
	}
	args = append(args,
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0:s=x",
		input,
	)
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
			return 0, 0, fmt.Errorf("ffprobe failed: %w: %s", err, msg)
		}
		return 0, 0, fmt.Errorf("ffprobe failed: %w", err)
	}
	line := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	widthRaw, heightRaw, ok := strings.Cut(line, "x")
	if !ok {
		return 0, 0, fmt.Errorf("ffprobe returned unexpected dimensions %q", line)
	}
	width, err := strconv.Atoi(strings.TrimSpace(widthRaw))
	if err != nil {
		return 0, 0, fmt.Errorf("parse ffprobe width %q: %w", widthRaw, err)
	}
	height, err := strconv.Atoi(strings.TrimSpace(heightRaw))
	if err != nil {
		return 0, 0, fmt.Errorf("parse ffprobe height %q: %w", heightRaw, err)
	}
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("ffprobe returned invalid dimensions %dx%d", width, height)
	}
	return width, height, nil
}

func concatMP4(ctx context.Context, parts []string, output string, logf func(string, ...any)) error {
	if len(parts) == 0 {
		return errors.New("no trimmed parts to concatenate")
	}
	if len(parts) == 1 {
		if logf != nil {
			logf("copy single part src=%q dst=%q", parts[0], output)
		}
		return copyFile(output, parts[0])
	}
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return err
	}
	listPath := filepath.Join(filepath.Dir(parts[0]), "concat.txt")
	if err := writeConcatList(listPath, parts); err != nil {
		return err
	}
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		"-movflags", "+faststart",
		output,
	}
	return runFFmpeg(ctx, ffmpeg, args, logf)
}

func runFFmpeg(ctx context.Context, ffmpeg string, args []string, logf func(string, ...any)) error {
	if logf != nil {
		logf("ffmpeg command: %s %s", ffmpeg, strings.Join(args, " "))
	}
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
	if logf != nil {
		if msg := strings.TrimSpace(stdout.String()); msg != "" {
			logf("ffmpeg stdout: %s", msg)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			logf("ffmpeg stderr: %s", msg)
		}
	}
	return nil
}

func writeConcatList(path string, parts []string) error {
	var b strings.Builder
	for _, part := range parts {
		abs, err := filepath.Abs(part)
		if err != nil {
			return err
		}
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(abs, "'", "'\\''"))
		b.WriteString("'\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func copyFile(dst string, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
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

func findFFprobe() (string, error) {
	if override := os.Getenv("NVRCLIP_FFPROBE"); override != "" {
		return override, nil
	}
	if ffmpegOverride := os.Getenv("NVRCLIP_FFMPEG"); ffmpegOverride != "" {
		name := "ffprobe"
		if strings.EqualFold(filepath.Ext(ffmpegOverride), ".exe") {
			name = "ffprobe.exe"
		}
		candidate := filepath.Join(filepath.Dir(ffmpegOverride), name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", errors.New("ffprobe not found; install ffmpeg or set NVRCLIP_FFPROBE")
	}
	return path, nil
}

func evenDimension(n int) int {
	if n > 1 && n%2 != 0 {
		return n - 1
	}
	return n
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

type logger struct {
	Path string
	file *os.File
}

func newLogger(path string) (*logger, error) {
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &logger{Path: path, file: file}, nil
}

func (l *logger) Printf(format string, args ...any) {
	if l == nil || l.file == nil {
		return
	}
	line := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.file, "%s %s\n", time.Now().Format(time.RFC3339), line)
}

func (l *logger) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = l.file.Close()
	l.file = nil
}
