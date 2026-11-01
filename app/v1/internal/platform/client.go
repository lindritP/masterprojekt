package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls another service's internal API. It propagates the correlation
// id and the subject from the context (FR-014, FR-103) and turns error
// responses back into problems, so a handler can pass them through unchanged.
type Client struct {
	base    string
	service string
	http    *http.Client
}

// NewClient returns a client for the service at base, identifying itself as service.
func NewClient(base, service string, timeout time.Duration) *Client {
	return &Client{base: strings.TrimSuffix(base, "/"), service: service, http: &http.Client{Timeout: timeout}}
}

// Response is a successful upstream response.
type Response struct {
	Status int
	Header http.Header
}

// Do sends body as JSON (if non-nil) and decodes a 2xx response into out (if
// non-nil). A non-2xx response is returned as a *Problem; an unreachable
// upstream as UPSTREAM_UNAVAILABLE.
func (c *Client) Do(ctx context.Context, method, path string, header http.Header, body, out any) (Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return Response{}, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(HeaderService, c.service)
	if id := CorrelationID(ctx); id != "" {
		req.Header.Set(HeaderCorrelationID, id)
	}
	if s, ok := SubjectFrom(ctx); ok {
		req.Header.Set(HeaderSubjectID, s.ID)
		req.Header.Set(HeaderSubjectRoles, strings.Join(s.Roles, ","))
		req.Header.Set(HeaderAuthLevel, s.AuthLevel)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return Response{}, ctx.Err()
		}
		return Response{}, NewProblem(CodeUpstreamUnavailable, "%s %s: %v", method, path, err)
	}
	defer drain(resp.Body)
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return Response{Status: resp.StatusCode, Header: resp.Header}, DecodeProblem(resp.StatusCode, data)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return Response{}, fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return Response{Status: resp.StatusCode, Header: resp.Header}, nil
}

// IsTransient reports whether err is worth retrying: an unreachable upstream
// or a 5xx response.
func IsTransient(err error) bool {
	if p, ok := errors.AsType[*Problem](err); ok {
		return p.Status >= 500
	}
	return err != nil
}
