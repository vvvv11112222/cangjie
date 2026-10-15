package capture

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client speaks the MediaMTX v1.15.3 v3 Control API.
type Client struct {
	base, secret string
	http         *http.Client
}
type Path struct {
	Name   string   `json:"name"`
	Ready  bool     `json:"ready"`
	Tracks []string `json:"tracks"`
	Source *struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	} `json:"source"`
}

func NewClient(base, secret string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("invalid capture control URL")
	}
	return &Client{base: strings.TrimRight(base, "/"), secret: secret, http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) request(ctx context.Context, method, path string, out any) error {
	r, e := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if e != nil {
		return e
	}
	r.SetBasicAuth("capture-control", c.secret)
	res, e := c.http.Do(r)
	if e != nil {
		return unavailable()
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return missing()
	}
	if res.StatusCode == 400 {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&body)
		if body.Error == "session not found" || body.Error == "path not found" {
			return missing()
		}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return unavailable()
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
	}
	return nil
}
func (c *Client) Path(ctx context.Context, path string) (Path, error) {
	var p Path
	e := c.request(ctx, "GET", "/v3/paths/get/"+path, &p)
	return p, e
}
func (c *Client) Kick(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	e := c.request(ctx, "POST", "/v3/webrtcsessions/kick/"+url.PathEscape(id), nil)
	if isMissing(e) {
		return nil
	}
	return e
}
func (c *Client) ClosePath(ctx context.Context, path string) error {
	var list struct {
		PageCount int                         `json:"pageCount"`
		Items     []struct{ ID, Path string } `json:"items"`
	}
	if e := c.request(ctx, "GET", "/v3/webrtcsessions/list?itemsPerPage=1000", &list); e != nil {
		return e
	}
	if list.PageCount > 1 {
		return unavailable()
	}
	for _, v := range list.Items {
		if v.Path == path {
			if e := c.Kick(ctx, v.ID); e != nil {
				return e
			}
		}
	}
	return nil
}
