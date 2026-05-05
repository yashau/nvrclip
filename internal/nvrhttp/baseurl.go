package nvrhttp

import (
	"fmt"
	"net/url"
	"strings"
)

type BaseURLs struct {
	URLs []string
}

func ParseBaseURLs(raw string) (BaseURLs, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return BaseURLs{}, fmt.Errorf("base URL is empty")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return BaseURLs{}, err
		}
		if u.Scheme == "" || u.Host == "" {
			return BaseURLs{}, fmt.Errorf("invalid base URL %q", raw)
		}
		return BaseURLs{URLs: []string{strings.TrimRight(u.String(), "/")}}, nil
	}
	return BaseURLs{URLs: []string{"https://" + raw, "http://" + raw}}, nil
}
