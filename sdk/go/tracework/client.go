// SPDX-License-Identifier: GPL-3.0-only
// Package tracework is the native blocking HTTP client. Evidence integers and
// nanosecond timestamps use strings; response values retain json.RawMessage.
package tracework

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(base, token string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("absolute server URL without credentials/query/fragment required")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && (u.Scheme != "http" || !(u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return nil, fmt.Errorf("use HTTPS for remote servers; plaintext is loopback-only")
	}
	return &Client{strings.TrimRight(base, "/"), token, &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Request(ctx context.Context, method, path string, in, out any) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf("relative API path required")
	}
	var body []byte
	var e error
	if in != nil {
		body, e = json.Marshal(in)
		if e != nil {
			return e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v1"+path, bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	r, e := c.HTTP.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	data, e := io.ReadAll(io.LimitReader(r.Body, (34<<20)+1))
	if e != nil {
		return e
	}
	if len(data) > 34<<20 {
		return fmt.Errorf("response exceeds 34 MiB")
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return fmt.Errorf("TRACEWORK HTTP %d: %s", r.StatusCode, data)
	}
	if out != nil {
		d := json.NewDecoder(bytes.NewReader(data))
		d.UseNumber()
		return d.Decode(out)
	}
	return nil
}

type Project struct {
	ID      string             `json:"id"`
	Name    string             `json:"name"`
	Sources []SourceDefinition `json:"sources"`
}
type SourceDefinition struct {
	ID      string   `json:"id,omitempty"`
	Name    string   `json:"name"`
	Clock   string   `json:"clock_domain"`
	StaleMS int64    `json:"stale_ms"`
	Events  []Schema `json:"events"`
}
type Schema struct {
	ID      string  `json:"id,omitempty"`
	Name    string  `json:"name"`
	Version int     `json:"version"`
	Fields  []Field `json:"fields"`
}
type Field struct {
	ID   string   `json:"id,omitempty"`
	Name string   `json:"name"`
	Type string   `json:"type"`
	Unit string   `json:"unit"`
	Enum []string `json:"enum,omitempty"`
}
type Recording struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}
type Event struct {
	ID          string                     `json:"id,omitempty"`
	Session     string                     `json:"session_id,omitempty"`
	Ordinal     int64                      `json:"ordinal,omitempty"`
	Source      string                     `json:"source_id"`
	Schema      string                     `json:"schema_id"`
	Version     int                        `json:"schema_version"`
	Boot        string                     `json:"boot_id"`
	Sequence    string                     `json:"sequence"`
	Timestamp   string                     `json:"timestamp_ns"`
	Received    string                     `json:"received_ns,omitempty"`
	Clock       string                     `json:"clock_domain"`
	Sync        string                     `json:"sync_quality"`
	Uncertainty *string                    `json:"uncertainty_ns"`
	Gap         bool                       `json:"gap_before"`
	Values      map[string]json.RawMessage `json:"values"`
	Message     string                     `json:"message,omitempty"`
	Level       string                     `json:"level,omitempty"`
}

func (c *Client) CreateProject(ctx context.Context, name string) (Project, error) {
	var p Project
	e := c.Request(ctx, "POST", "/projects", map[string]any{"name": name, "sources": []any{}}, &p)
	return p, e
}
func (c *Client) DefineSource(ctx context.Context, pid string, src SourceDefinition) (SourceDefinition, error) {
	var p Project
	e := c.Request(ctx, "POST", "/projects/"+pid+"/sources", src, &p)
	if e != nil {
		return SourceDefinition{}, e
	}
	if len(p.Sources) == 0 {
		return SourceDefinition{}, fmt.Errorf("server returned no source")
	}
	return p.Sources[len(p.Sources)-1], nil
}
func (c *Client) StartRecording(ctx context.Context, pid, name string) (Recording, error) {
	var r Recording
	e := c.Request(ctx, "POST", "/projects/"+pid+"/sessions", map[string]string{"name": name}, &r)
	return r, e
}
func (c *Client) StopRecording(ctx context.Context, sid string) error {
	return c.Request(ctx, "POST", "/sessions/"+sid+"/stop", map[string]any{}, nil)
}
func (c *Client) Events(ctx context.Context, sid string, visit func(Event) error) error {
	var after int64
	for {
		var page struct {
			Events []Event `json:"events"`
			More   bool    `json:"more"`
			Next   int64   `json:"next"`
		}
		if e := c.Request(ctx, "GET", "/sessions/"+sid+"/events?after="+strconv.FormatInt(after, 10), nil, &page); e != nil {
			return e
		}
		for _, ev := range page.Events {
			if e := visit(ev); e != nil {
				return e
			}
		}
		if !page.More {
			return nil
		}
		if page.Next <= after {
			return fmt.Errorf("recording pagination did not advance")
		}
		after = page.Next
	}
}
func ExactUint64(n uint64) json.RawMessage { b, _ := json.Marshal(strconv.FormatUint(n, 10)); return b }
func ExactInt64(n int64) json.RawMessage   { b, _ := json.Marshal(strconv.FormatInt(n, 10)); return b }

type Source struct {
	mu        sync.Mutex
	client    *Client
	project   string
	template  Event
	sequence  uint64
	pending   *Event
	exhausted bool
	Recording string
}

func (c *Client) Source(project string, src SourceDefinition, sc Schema) *Source {
	random := make([]byte, 16)
	if _, e := rand.Read(random); e != nil {
		panic(e)
	}
	return &Source{client: c, project: project, template: Event{Source: src.ID, Schema: sc.ID, Version: sc.Version, Boot: hex.EncodeToString(random), Clock: src.Clock, Sync: "unsynchronized"}}
}
func (s *Source) Emit(ctx context.Context, values map[string]json.RawMessage, timestampNS int64, gap bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending != nil {
		return fmt.Errorf("unresolved send; retry the same observation first")
	}
	if timestampNS < 0 || s.exhausted {
		return fmt.Errorf("invalid timestamp or exhausted producer sequence")
	}
	ev := s.template
	ev.Sequence = strconv.FormatUint(s.sequence, 10)
	ev.Timestamp = strconv.FormatInt(timestampNS, 10)
	ev.Gap = gap
	ev.Values = map[string]json.RawMessage{}
	for k, v := range values {
		ev.Values[k] = append(json.RawMessage{}, v...)
	}
	s.pending = &ev
	return s.retry(ctx)
}
func (s *Source) Retry(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retry(ctx)
}
func (s *Source) retry(ctx context.Context) error {
	if s.pending == nil {
		return fmt.Errorf("no pending observation")
	}
	body := map[string]any{"events": []Event{*s.pending}, "require_recording": true}
	if s.Recording != "" {
		body["session_id"] = s.Recording
	}
	if e := s.client.Request(ctx, "POST", "/projects/"+s.project+"/events", body, nil); e != nil {
		return e
	}
	s.pending = nil
	if s.sequence == math.MaxUint64 {
		s.exhausted = true
	} else {
		s.sequence++
	}
	return nil
}
