package dahua

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

type digestTransport struct {
	username string
	password string
	base     http.RoundTripper
	nonceCnt atomic.Uint32
}

func (t *digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	first := req.Clone(req.Context())
	resp, err := base.RoundTrip(first)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	challenge := resp.Header.Get("WWW-Authenticate")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	params, ok := parseDigestChallenge(challenge)
	if !ok {
		return resp, nil
	}

	second := req.Clone(req.Context())
	second.Header.Set("Authorization", t.authorization(second, params))
	return base.RoundTrip(second)
}

func parseDigestChallenge(header string) (map[string]string, bool) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(header), "digest ") {
		return nil, false
	}
	header = strings.TrimSpace(header[len("Digest "):])
	out := make(map[string]string)
	for _, part := range splitComma(header) {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"`)
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	if out["realm"] == "" || out["nonce"] == "" {
		return nil, false
	}
	return out, true
}

func splitComma(s string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case ',':
			if inQuote {
				b.WriteRune(r)
			} else {
				parts = append(parts, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func (t *digestTransport) authorization(req *http.Request, p map[string]string) string {
	realm := p["realm"]
	nonce := p["nonce"]
	opaque := p["opaque"]
	qop := chooseQOP(p["qop"])
	uri := req.URL.RequestURI()
	nc := fmt.Sprintf("%08x", t.nonceCnt.Add(1))
	cnonce := randomHex(16)

	ha1 := md5Hex(t.username + ":" + realm + ":" + t.password)
	ha2 := md5Hex(req.Method + ":" + uri)
	response := md5Hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)

	fields := []string{
		`username="` + quote(t.username) + `"`,
		`realm="` + quote(realm) + `"`,
		`nonce="` + quote(nonce) + `"`,
		`uri="` + quote(uri) + `"`,
		`response="` + response + `"`,
		`qop=` + qop,
		`nc=` + nc,
		`cnonce="` + cnonce + `"`,
	}
	if opaque != "" {
		fields = append(fields, `opaque="`+quote(opaque)+`"`)
	}
	return "Digest " + strings.Join(fields, ",")
}

func chooseQOP(raw string) string {
	for _, qop := range strings.Split(raw, ",") {
		if strings.TrimSpace(qop) == "auth" {
			return "auth"
		}
	}
	return "auth"
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(int64(tinyRand()), 16)
	}
	return hex.EncodeToString(buf)
}

func tinyRand() uint32 {
	var x atomic.Uint32
	return x.Add(1)
}

func quote(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
