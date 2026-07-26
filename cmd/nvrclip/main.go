package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yashau/nvrclip/internal/clip"
	"github.com/yashau/nvrclip/internal/config"
	"github.com/yashau/nvrclip/internal/dahua"
	"github.com/yashau/nvrclip/internal/hikvision"
	"github.com/yashau/nvrclip/internal/nvr"
)

const usage = `nvrclip exports exact clips from NVR recordings.

Usage:
  nvrclip grab <channel alias> --from "2026-05-06 14:50" --to "2026-05-06 15:20" [flags]
  nvrclip download <nvr> --channel front-door --around "2026-05-05 14:05" --minutes 10 [flags]
  nvrclip download <nvr> --channel 1 --from "2026-05-05 14:00" --to "2026-05-05 14:10" [flags]

Flags:
  --config path        TOML config path (default: nvrclip.toml)
  --out dir           output directory (default: current directory)
  --work-dir dir      temporary work directory
  --keep-temp         keep downloaded temporary files
  --download-only     download NVR chunks but skip ffmpeg output
  --auto-time-offset compare the NVR clock with the PC and adjust request times
  --mode copy|exact   copy remux or exact re-encode trim (default: copy)
  --format copy|exact alias for --mode
`

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nvrclip: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "grab":
		return runGrab(ctx, args[1:])
	case "download":
		return runDownload(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func runGrab(ctx context.Context, args []string) error {
	aliasParts, flagArgs := splitAliasAndFlags(args)
	if len(aliasParts) == 0 {
		return errors.New("grab needs a channel alias")
	}
	aliasName := strings.Join(aliasParts, " ")

	fs := flag.NewFlagSet("grab", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "nvrclip.toml", "TOML config path")
	outDir := fs.String("out", ".", "output directory")
	workDir := fs.String("work-dir", "", "temporary work directory")
	keepTemp := fs.Bool("keep-temp", false, "keep downloaded temporary files")
	downloadOnly := fs.Bool("download-only", false, "download NVR chunks but skip ffmpeg output")
	autoTimeOffset := fs.Bool("auto-time-offset", false, "compare the NVR clock with the PC and adjust request times")
	mode := fs.String("mode", "copy", "copy or exact")
	format := fs.String("format", "", "alias for --mode; copy or exact")
	fromRaw := fs.String("from", "", "clip start time")
	toRaw := fs.String("to", "", "clip end time")
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	selectedMode, err := resolveModeFlag(fs, *mode, *format)
	if err != nil {
		return err
	}
	if *fromRaw == "" || *toRaw == "" {
		return errors.New("grab needs --from and --to")
	}

	from, err := parseLocalTime(*fromRaw)
	if err != nil {
		return fmt.Errorf("parse --from: %w", err)
	}
	to, err := parseLocalTime(*toRaw)
	if err != nil {
		return fmt.Errorf("parse --to: %w", err)
	}
	if !to.After(from) {
		return errors.New("--to must be after --from")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	channel, err := cfg.ResolveGlobalChannel(aliasName)
	if err != nil {
		return err
	}
	nvrCfg, ok := cfg.NVRs[channel.NVR]
	if !ok {
		return fmt.Errorf("alias %q references unknown NVR %q", aliasName, channel.NVR)
	}

	frameRate := channel.FrameRate
	if frameRate == 0 {
		frameRate = nvrCfg.FrameRate
	}

	return runClip(ctx, clipRequest{
		NVRName:        channel.NVR,
		NVR:            nvrCfg,
		Label:          aliasName,
		Channel:        channel.Number,
		From:           from,
		To:             to,
		OutputDir:      *outDir,
		WorkDir:        *workDir,
		KeepTemp:       *keepTemp,
		DownloadOnly:   *downloadOnly,
		AutoTimeOffset: *autoTimeOffset || nvrCfg.AutoTimeOffset,
		Mode:           selectedMode,
		FrameRate:      frameRate,
	})
}

func runDownload(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("download needs an NVR name")
	}
	nvrName := args[0]

	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "nvrclip.toml", "TOML config path")
	outDir := fs.String("out", ".", "output directory")
	workDir := fs.String("work-dir", "", "temporary work directory")
	keepTemp := fs.Bool("keep-temp", false, "keep downloaded temporary files")
	downloadOnly := fs.Bool("download-only", false, "download NVR chunks but skip ffmpeg output")
	autoTimeOffset := fs.Bool("auto-time-offset", false, "compare the NVR clock with the PC and adjust request times")
	mode := fs.String("mode", "copy", "copy or exact")
	format := fs.String("format", "", "alias for --mode; copy or exact")
	channelRaw := fs.String("channel", "", "channel alias or number")
	aroundRaw := fs.String("around", "", "center time")
	minutes := fs.Float64("minutes", 0, "minutes around center time")
	fromRaw := fs.String("from", "", "clip start time")
	toRaw := fs.String("to", "", "clip end time")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	selectedMode, err := resolveModeFlag(fs, *mode, *format)
	if err != nil {
		return err
	}
	if *channelRaw == "" {
		return errors.New("download needs --channel")
	}

	var from, to time.Time
	if *aroundRaw != "" {
		if *minutes <= 0 {
			return errors.New("--around needs --minutes greater than 0")
		}
		around, err := parseLocalTime(*aroundRaw)
		if err != nil {
			return fmt.Errorf("parse --around: %w", err)
		}
		half := time.Duration((*minutes * float64(time.Minute)) / 2)
		from = around.Add(-half)
		to = around.Add(half)
	} else {
		if *fromRaw == "" || *toRaw == "" {
			return errors.New("download needs either --around with --minutes, or --from and --to")
		}
		var err error
		from, err = parseLocalTime(*fromRaw)
		if err != nil {
			return fmt.Errorf("parse --from: %w", err)
		}
		to, err = parseLocalTime(*toRaw)
		if err != nil {
			return fmt.Errorf("parse --to: %w", err)
		}
	}
	if !to.After(from) {
		return errors.New("end time must be after start time")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	nvrCfg, ok := cfg.NVRs[nvrName]
	if !ok {
		return fmt.Errorf("unknown NVR %q", nvrName)
	}
	channel, err := cfg.ResolveChannel(nvrName, *channelRaw)
	if err != nil {
		return err
	}
	label := channel.Name
	if label == "" {
		label = fmt.Sprintf("channel %d", channel.Number)
	}
	outputLabel := nvrName + " " + label
	if _, err := parseChannelNumber(*channelRaw); err == nil {
		outputLabel = fmt.Sprintf("%s channel %d", nvrName, channel.Number)
	}
	frameRate := channel.FrameRate
	if frameRate == 0 {
		frameRate = nvrCfg.FrameRate
	}

	return runClip(ctx, clipRequest{
		NVRName:        nvrName,
		NVR:            nvrCfg,
		Label:          outputLabel,
		Channel:        channel.Number,
		From:           from,
		To:             to,
		OutputDir:      *outDir,
		WorkDir:        *workDir,
		KeepTemp:       *keepTemp,
		DownloadOnly:   *downloadOnly,
		AutoTimeOffset: *autoTimeOffset || nvrCfg.AutoTimeOffset,
		Mode:           selectedMode,
		FrameRate:      frameRate,
	})
}

type clipRequest struct {
	NVRName        string
	NVR            config.NVR
	Label          string
	Channel        int
	From           time.Time
	To             time.Time
	OutputDir      string
	WorkDir        string
	KeepTemp       bool
	DownloadOnly   bool
	AutoTimeOffset bool
	Mode           string
	FrameRate      float64
}

func runClip(ctx context.Context, req clipRequest) error {
	password, err := req.NVR.ResolvePassword()
	if err != nil {
		return err
	}
	adapter, err := newAdapter(req.NVR, password)
	if err != nil {
		return err
	}
	if req.AutoTimeOffset {
		clock, ok := adapter.(nvr.Clock)
		if !ok {
			return fmt.Errorf("NVR %q does not support automatic time offset", req.NVRName)
		}
		sample, err := nvr.MeasureClockOffset(ctx, clock, time.Now)
		if err != nil {
			return fmt.Errorf("measure NVR %q clock offset: %w", req.NVRName, err)
		}
		fmt.Fprintf(
			os.Stdout,
			"NVR clock %s, PC clock %s, offset %s; querying %s to %s\n",
			sample.NVRTime.Format("2006-01-02 15:04:05"),
			sample.PCTime.Format("2006-01-02 15:04:05"),
			signedDuration(sample.Offset),
			req.From.Add(sample.Offset).Format("2006-01-02 15:04:05"),
			req.To.Add(sample.Offset).Format("2006-01-02 15:04:05"),
		)
		adapter = nvr.WithClockOffset(adapter, sample.Offset)
	}
	frameRate := req.FrameRate
	if frameRate == 0 {
		frameRate = 25
	}
	result, err := clip.Run(ctx, clip.Job{
		Adapter:      adapter,
		Alias:        req.Label,
		Channel:      req.Channel,
		From:         req.From,
		To:           req.To,
		OutputDir:    req.OutputDir,
		WorkDir:      req.WorkDir,
		KeepTemp:     req.KeepTemp,
		DownloadOnly: req.DownloadOnly,
		Mode:         clip.Mode(req.Mode),
		FrameRate:    frameRate,
		Stdout:       os.Stdout,
	})
	if err != nil {
		if result.LogPath != "" {
			fmt.Fprintf(os.Stderr, "log %s\n", result.LogPath)
		}
		return err
	}
	if result.OutputPath != "" {
		fmt.Fprintf(os.Stdout, "wrote %s\n", result.OutputPath)
	}
	if result.WorkDir != "" && (req.KeepTemp || req.DownloadOnly) {
		abs, _ := filepath.Abs(result.WorkDir)
		fmt.Fprintf(os.Stdout, "kept work dir %s\n", abs)
	}
	return nil
}

func signedDuration(value time.Duration) string {
	if value > 0 {
		return "+" + value.String()
	}
	return value.String()
}

func newAdapter(nvrCfg config.NVR, password string) (nvr.Adapter, error) {
	switch strings.ToLower(nvrCfg.Type) {
	case "dahua":
		return dahua.New(dahua.Options{
			BaseURL:     nvrCfg.BaseURL,
			Username:    nvrCfg.Username,
			Password:    password,
			Timeout:     nvrCfg.TimeoutDuration(),
			InsecureTLS: nvrCfg.InsecureTLS,
		})
	case "hikvision":
		return hikvision.New(hikvision.Options{
			BaseURL:     nvrCfg.BaseURL,
			Username:    nvrCfg.Username,
			Password:    password,
			Timeout:     nvrCfg.TimeoutDuration(),
			InsecureTLS: nvrCfg.InsecureTLS,
		})
	default:
		return nil, fmt.Errorf("NVR %q has unsupported type %q", nvrCfg.Name, nvrCfg.Type)
	}
}

func splitAliasAndFlags(args []string) ([]string, []string) {
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			return args[:i], args[i:]
		}
	}
	return args, nil
}

func parseChannelNumber(raw string) (int, error) {
	return strconv.Atoi(raw)
}

func resolveModeFlag(fs *flag.FlagSet, modeValue string, formatValue string) (string, error) {
	modeSet := false
	formatSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "mode":
			modeSet = true
		case "format":
			formatSet = true
		}
	})
	if formatSet {
		if modeSet && modeValue != formatValue {
			return "", fmt.Errorf("--mode %q conflicts with --format %q", modeValue, formatValue)
		}
		modeValue = formatValue
	}
	switch clip.Mode(modeValue) {
	case clip.ModeCopy, clip.ModeExact:
		return modeValue, nil
	default:
		return "", fmt.Errorf("unsupported format/mode %q; use copy or exact", modeValue)
	}
}

func parseLocalTime(raw string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	var lastErr error
	for _, layout := range layouts {
		var (
			t   time.Time
			err error
		)
		if layout == time.RFC3339 {
			t, err = time.Parse(layout, raw)
		} else {
			t, err = time.ParseInLocation(layout, raw, time.Local)
		}
		if err == nil {
			return t, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}
