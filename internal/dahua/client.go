package dahua

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yashau/nvrclip/internal/nvr"
	"github.com/yashau/nvrclip/internal/nvrhttp"
	"github.com/yashau/nvrclip/internal/timefmt"
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
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	return &Client{
		baseURLs: bases.URLs,
		http: nvrhttp.NewDigestClient(nvrhttp.DigestOptions{
			Username:    opts.Username,
			Password:    opts.Password,
			Timeout:     timeout,
			InsecureTLS: opts.InsecureTLS,
		}),
	}, nil
}

func (c *Client) CurrentTime(ctx context.Context) (time.Time, error) {
	var lastErr error
	for _, baseURL := range c.baseURLs {
		text, err := c.getTextURL(ctx, endpointPairs(baseURL, "/cgi-bin/global.cgi", []queryPair{
			{"action", "getCurrentTime"},
		}))
		if err != nil {
			lastErr = err
			continue
		}
		current, err := parseCurrentTime(text)
		if err == nil {
			return current, nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
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

func parseCurrentTime(text string) (time.Time, error) {
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key != "result" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		current, err := time.ParseInLocation(timefmt.DahuaLayout, value, time.Local)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse dahua current time %q: %w", value, err)
		}
		return current, nil
	}
	return time.Time{}, fmt.Errorf("dahua getCurrentTime returned no result: %q", text)
}

func (c *Client) searchBase(ctx context.Context, baseURL string, channel int, from time.Time, to time.Time) ([]nvr.Segment, error) {
	object, err := c.createFinder(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	defer c.closeFinder(context.Background(), baseURL, object)

	findURL := endpointPairs(baseURL, "/cgi-bin/mediaFileFind.cgi", []queryPair{
		{"action", "findFile"},
		{"object", object},
		{"condition.Channel", strconv.Itoa(channel)},
		{"condition.StartTime", timefmt.Dahua(from)},
		{"condition.EndTime", timefmt.Dahua(to)},
		{"condition.Types[0]", "dav"},
	})
	if _, err := c.getTextURL(ctx, findURL); err != nil {
		return nil, fmt.Errorf("dahua findFile: %w", err)
	}

	var segments []nvr.Segment
	for {
		nextURL := endpointPairs(baseURL, "/cgi-bin/mediaFileFind.cgi", []queryPair{
			{"action", "findNextFile"},
			{"object", object},
			{"count", "128"},
		})
		text, err := c.getTextURL(ctx, nextURL)
		if err != nil {
			return nil, fmt.Errorf("dahua findNextFile: %w", err)
		}
		found, batch, err := parseFindNext(text)
		if err != nil {
			return nil, err
		}
		segments = append(segments, batch...)
		if found == 0 || len(batch) < 128 {
			break
		}
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
			req.Logf("dahua download try base_url=%q", baseURL)
		}
		result, err := c.downloadBase(ctx, baseURL, req)
		if err == nil {
			return result, nil
		}
		if req.Logf != nil {
			req.Logf("dahua download base_url=%q error=%v", baseURL, err)
		}
		lastErr = err
	}
	return nvr.DownloadResult{}, lastErr
}

func (c *Client) downloadBase(ctx context.Context, baseURL string, req nvr.DownloadRequest) (nvr.DownloadResult, error) {
	downloadURL := endpointPairs(baseURL, "/cgi-bin/loadfile.cgi", []queryPair{
		{"action", "startLoad"},
		{"channel", strconv.Itoa(req.Channel)},
		{"startTime", timefmt.Dahua(req.From)},
		{"endTime", timefmt.Dahua(req.To)},
		{"subtype", "0"},
	})
	if req.Logf != nil {
		req.Logf("dahua download url=%s", downloadURL)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nvr.DownloadResult{}, err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nvr.DownloadResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nvr.DownloadResult{}, fmt.Errorf("dahua download failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	out, err := os.Create(req.Path)
	if err != nil {
		return nvr.DownloadResult{}, err
	}
	defer out.Close()
	reader := io.Reader(resp.Body)
	if req.Progress != nil {
		reader = &progressReader{
			reader:   resp.Body,
			total:    resp.ContentLength,
			progress: req.Progress,
		}
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
	return nvr.DownloadResult{From: req.From, To: req.To, ForceFrameRate: true}, nil
}

func (c *Client) createFinder(ctx context.Context, baseURL string) (string, error) {
	text, err := c.getTextURL(ctx, endpointPairs(baseURL, "/cgi-bin/mediaFileFind.cgi", []queryPair{
		{"action", "factory.create"},
	}))
	if err != nil {
		return "", fmt.Errorf("dahua factory.create: %w", err)
	}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k == "result" && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("dahua factory.create returned no object: %q", text)
}

func (c *Client) closeFinder(ctx context.Context, baseURL string, object string) {
	_, _ = c.getTextURL(ctx, endpointPairs(baseURL, "/cgi-bin/mediaFileFind.cgi", []queryPair{
		{"action", "close"},
		{"object", object},
	}))
}

func (c *Client) getText(ctx context.Context, path string, values url.Values) (string, error) {
	return c.getTextURL(ctx, endpoint(c.baseURLs[0], path, values))
}

func (c *Client) getTextURL(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func endpoint(baseURL string, path string, values url.Values) string {
	u := baseURL + path
	if len(values) > 0 {
		u += "?" + values.Encode()
	}
	return u
}

type queryPair struct {
	key   string
	value string
}

func endpointPairs(baseURL string, path string, pairs []queryPair) string {
	u := baseURL + path
	if len(pairs) == 0 {
		return u
	}
	var b strings.Builder
	b.WriteString(u)
	b.WriteByte('?')
	for i, pair := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(queryEscape(pair.key))
		b.WriteByte('=')
		b.WriteString(queryEscape(pair.value))
	}
	return b.String()
}

func queryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

var itemKey = regexp.MustCompile(`^items\[(\d+)\]\.([^=]+)$`)

func parseFindNext(text string) (int, []nvr.Segment, error) {
	found := 0
	items := map[int]map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k == "found" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return 0, nil, fmt.Errorf("parse found=%q: %w", v, err)
			}
			found = n
			continue
		}
		m := itemKey.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		i, _ := strconv.Atoi(m[1])
		if items[i] == nil {
			items[i] = map[string]string{}
		}
		items[i][m[2]] = v
	}
	if err := scanner.Err(); err != nil {
		return 0, nil, err
	}

	indexes := make([]int, 0, len(items))
	for i := range items {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	segments := make([]nvr.Segment, 0, len(indexes))
	for _, i := range indexes {
		seg, err := parseSegment(items[i])
		if err != nil {
			return 0, nil, fmt.Errorf("parse item %d: %w", i, err)
		}
		segments = append(segments, seg)
	}
	return found, segments, nil
}

func parseSegment(values map[string]string) (nvr.Segment, error) {
	start, err := time.ParseInLocation(timefmt.DahuaLayout, values["StartTime"], time.Local)
	if err != nil {
		return nvr.Segment{}, err
	}
	end, err := time.ParseInLocation(timefmt.DahuaLayout, values["EndTime"], time.Local)
	if err != nil {
		return nvr.Segment{}, err
	}
	channel, _ := strconv.Atoi(values["Channel"])
	return nvr.Segment{
		Channel:  channel,
		Start:    start,
		End:      end,
		FilePath: values["FilePath"],
		Type:     values["Type"],
		Stream:   values["VideoStream"],
	}, nil
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
