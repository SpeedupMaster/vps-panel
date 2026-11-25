package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	maxCheckBody = 384 << 10 // cap concurrent HTML reads well under 16 MiB GOMEMLIMIT
	ua           = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"
)

var sharedCheckTransport = &http.Transport{
	MaxIdleConns:        8,
	MaxIdleConnsPerHost: 2,
	IdleConnTimeout:     15 * time.Second,
	ForceAttemptHTTP2:   true,
}

func httpClient(timeout time.Duration, follow bool) *http.Client {
	c := &http.Client{Timeout: timeout, Transport: sharedCheckTransport}
	if !follow {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return c
}

func get(ctx context.Context, rawURL string, follow bool) (*http.Response, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := httpClient(15*time.Second, follow).Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxCheckBody))
	return resp, string(b), nil
}

// getJSON fetches rawURL and decodes the body into v, treating non-200 as an error.
func getJSON(ctx context.Context, rawURL string, v any) error {
	resp, body, err := get(ctx, rawURL, true)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(body), &e)
		if e.Error == "" {
			e.Error = e.Message
		}
		if resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToLower(e.Error), "api key") {
			return fmt.Errorf("API key 无效")
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("超出频率限制 (HTTP 429)")
		}
		if e.Error != "" {
			return fmt.Errorf("HTTP %d：%s", resp.StatusCode, e.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		return fmt.Errorf("响应解析失败")
	}
	return nil
}

func errText(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "超时"
	}
	return err.Error()
}
