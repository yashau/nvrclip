package hikvision

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yashau/nvrclip/internal/nvr"
	"github.com/yashau/nvrclip/internal/nvrhttp"
)

type Options struct {
	BaseURL     string
	Username    string
	Password    string
	Timeout     time.Duration
	InsecureTLS bool
}

type Client struct {
	baseURLs []string
	http     *http.Client
}

func New(opts Options) (*Client, error) {
	bases, err := nvrhttp.ParseBaseURLs(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURLs: bases.URLs,
		http: nvrhttp.NewDigestClient(nvrhttp.DigestOptions{
			Username:    opts.Username,
			Password:    opts.Password,
			Timeout:     opts.Timeout,
			InsecureTLS: opts.InsecureTLS,
		}),
	}, nil
}

func (c *Client) Search(ctx context.Context, channel int, from time.Time, to time.Time) ([]nvr.Segment, error) {
	var lastErr error
	for _, baseURL := range c.baseURLs {
		segments, err := c.searchBase(ctx, baseURL, channel, from, to)
		if err == nil {
			return segments, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (c *Client) searchBase(ctx context.Context, baseURL string, channel int, from time.Time, to time.Time) ([]nvr.Segment, error) {
	body := searchRequest(channel, from, to)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ISAPI/ContentMgmt/search", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("hikvision search failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	var result cmSearchResult
	if err := xml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if !result.ResponseStatus {
		return nil, fmt.Errorf("hikvision search failed: %s", result.ResponseStatusStrg)
	}

	segments := make([]nvr.Segment, 0, len(result.MatchList.Items))
	for _, item := range result.MatchList.Items {
		start, err := parseHikTime(item.TimeSpan.StartTime)
		if err != nil {
			return nil, fmt.Errorf("parse hikvision start time %q: %w", item.TimeSpan.StartTime, err)
		}
		end, err := parseHikTime(item.TimeSpan.EndTime)
		if err != nil {
			return nil, fmt.Errorf("parse hikvision end time %q: %w", item.TimeSpan.EndTime, err)
		}
		segments = append(segments, nvr.Segment{
			Channel:  channel,
			Start:    start,
			End:      end,
			FilePath: item.MediaSegment.PlaybackURI,
			Type:     "hikvision",
			Stream:   strconv.Itoa(trackID(channel)),
		})
	}
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Start.Before(segments[j].Start)
	})
	return segments, nil
}

func (c *Client) Download(ctx context.Context, req nvr.DownloadRequest) (nvr.DownloadResult, error) {
	var lastErr error
	for _, baseURL := range c.baseURLs {
		if req.Logf != nil {
			req.Logf("hikvision download try base_url=%q", baseURL)
		}
		result, err := c.downloadBase(ctx, baseURL, req)
		if err == nil {
			return result, nil
		}
		if req.Logf != nil {
			req.Logf("hikvision download base_url=%q error=%v", baseURL, err)
		}
		lastErr = err
	}
	return nvr.DownloadResult{}, lastErr
}

func (c *Client) downloadBase(ctx context.Context, baseURL string, req nvr.DownloadRequest) (nvr.DownloadResult, error) {
	playbackURI := req.Segment.FilePath
	if playbackURI == "" {
		return nvr.DownloadResult{}, fmt.Errorf("hikvision segment has no playback URI")
	}
	body := downloadRequest(playbackURI)
	if req.Logf != nil {
		req.Logf("hikvision download endpoint=%s playback_uri=%s", baseURL+"/ISAPI/ContentMgmt/download", playbackURI)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/ISAPI/ContentMgmt/download", strings.NewReader(body))
	if err != nil {
		return nvr.DownloadResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/xml")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nvr.DownloadResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nvr.DownloadResult{}, fmt.Errorf("hikvision download failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	out, err := os.Create(req.Path)
	if err != nil {
		return nvr.DownloadResult{}, err
	}
	defer out.Close()
	reader := io.Reader(resp.Body)
	if req.Progress != nil {
		reader = &progressReader{reader: resp.Body, total: resp.ContentLength, progress: req.Progress}
	}
	if _, err := io.Copy(out, reader); err != nil {
		return nvr.DownloadResult{}, err
	}
	if req.Progress != nil {
		info, err := out.Stat()
		if err == nil {
			req.Progress(nvr.Progress{Downloaded: info.Size(), Total: resp.ContentLength})
		}
	}
	return nvr.DownloadResult{From: req.Segment.Start, To: req.Segment.End}, nil
}

func searchRequest(channel int, from time.Time, to time.Time) string {
	id := fmt.Sprintf("{%08x-0000-0000-0000-000000000000}", time.Now().UnixNano())
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<CMSearchDescription>
  <searchID>%s</searchID>
  <trackList><trackID>%d</trackID></trackList>
  <timeSpanList><timeSpan><startTime>%s</startTime><endTime>%s</endTime></timeSpan></timeSpanList>
  <maxResults>128</maxResults>
  <searchResultPosition>0</searchResultPosition>
  <metadataList><metadataDescriptor>//recordType.meta.std-cgi.com</metadataDescriptor></metadataList>
</CMSearchDescription>`, id, trackID(channel), hikSearchTime(from), hikSearchTime(to))
}

func downloadRequest(playbackURI string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(playbackURI))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<downloadRequest version="1.0" xmlns="http://www.isapi.org/ver20/XMLSchema">
  <playbackURI>%s</playbackURI>
</downloadRequest>`, b.String())
}

func trackID(channel int) int {
	return channel*100 + 1
}

func hikSearchTime(t time.Time) string {
	return t.Format("2006-01-02T15:04:05Z07:00")
}

func parseHikTime(raw string) (time.Time, error) {
	if strings.HasSuffix(raw, "Z") {
		raw = strings.TrimSuffix(raw, "Z")
		return time.ParseInLocation("2006-01-02T15:04:05", raw, time.Local)
	}
	return time.Parse(time.RFC3339, raw)
}

type cmSearchResult struct {
	XMLName            xml.Name  `xml:"CMSearchResult"`
	ResponseStatus     bool      `xml:"responseStatus"`
	ResponseStatusStrg string    `xml:"responseStatusStrg"`
	MatchList          matchList `xml:"matchList"`
}

type matchList struct {
	Items []searchMatchItem `xml:"searchMatchItem"`
}

type searchMatchItem struct {
	TrackID      int          `xml:"trackID"`
	TimeSpan     timeSpan     `xml:"timeSpan"`
	MediaSegment mediaSegment `xml:"mediaSegmentDescriptor"`
}

type timeSpan struct {
	StartTime string `xml:"startTime"`
	EndTime   string `xml:"endTime"`
}

type mediaSegment struct {
	PlaybackURI string `xml:"playbackURI"`
}

type progressReader struct {
	reader     io.Reader
	total      int64
	downloaded int64
	progress   func(nvr.Progress)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.downloaded += int64(n)
		r.progress(nvr.Progress{Downloaded: r.downloaded, Total: r.total})
	}
	return n, err
}

func rewritePlaybackURI(raw string, from time.Time, to time.Time) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Set("starttime", from.Format("20060102T150405Z"))
	q.Set("endtime", to.Format("20060102T150405Z"))
	u.RawQuery = q.Encode()
	return u.String()
}
