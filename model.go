// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Version = "1.0.0"

type Field struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Type string   `json:"type"`
	Unit string   `json:"unit"`
	Enum []string `json:"enum,omitempty"`
}
type Schema struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Version int     `json:"version"`
	Fields  []Field `json:"fields"`
}
type DecoderMetadata struct {
	Kind       string `json:"kind"`
	SHA        string `json:"sha256"`
	Definition string `json:"definition"`
}
type Source struct {
	Decoder   *DecoderMetadata `json:"decoder,omitempty"`
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Clock     string           `json:"clock_domain"`
	StaleMS   int64            `json:"stale_ms"`
	Synthetic bool             `json:"synthetic"`
	Events    []Schema         `json:"events"`
}
type Project struct {
	ReadOnly    bool     `json:"read_only,omitempty"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Format      string   `json:"format"`
	Sample      bool     `json:"sample"`
	Sources     []Source `json:"sources"`
	Created     string   `json:"created_ns"`
}
type Event struct {
	ID            string                     `json:"id"`
	Source        string                     `json:"source_id"`
	Schema        string                     `json:"schema_id"`
	SchemaVersion int                        `json:"schema_version"`
	Boot          string                     `json:"boot_id"`
	Seq           string                     `json:"sequence"`
	Timestamp     string                     `json:"timestamp_ns"`
	Received      string                     `json:"received_ns"`
	Clock         string                     `json:"clock_domain"`
	Sync          string                     `json:"sync_quality"`
	Uncertainty   *string                    `json:"uncertainty_ns"`
	Values        map[string]json.RawMessage `json:"values"`
	Level         string                     `json:"level,omitempty"`
	Message       string                     `json:"message,omitempty"`
	Gap           bool                       `json:"gap_before"`
	Flags         []string                   `json:"flags,omitempty"`
	Ordinal       int64                      `json:"ordinal"`
	Session       string                     `json:"session_id,omitempty"`
}
type Session struct {
	Protected bool            `json:"protected"`
	Trashed   bool            `json:"trashed"`
	Origin    json.RawMessage `json:"origin,omitempty"`
	ID        string          `json:"id"`
	Project   string          `json:"project_id"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	Created   string          `json:"created_ns"`
	Ended     string          `json:"ended_ns"`
	Count     int64           `json:"count"`
	Snapshot  Project         `json:"project"`
}

var ident = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)
var decimal = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var signed = regexp.MustCompile(`^(0|-?[1-9][0-9]*)$`)

func id() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func now() string             { return strconv.FormatInt(time.Now().UnixNano(), 10) }
func validName(n string) bool { return strings.TrimSpace(n) != "" && len(n) <= 160 }
func validateProject(p *Project) error {
	if !validName(p.Name) {
		return fmt.Errorf("project name is required (up to 160 characters)")
	}
	if len(p.Description) > 4096 || len(p.Sources) > 64 {
		return fmt.Errorf("project limit exceeded")
	}
	if p.Format != "" && p.Format != "tracework.project.v1" {
		return fmt.Errorf("unsupported project format")
	}
	p.Format = "tracework.project.v1"
	used := map[string]bool{}
	schemaOwner := map[string]string{}
	fieldDefinition := map[string]string{}
	fieldOwner := map[string]string{}
	revisions := map[string]int{}
	for i := range p.Sources {
		s := &p.Sources[i]
		if s.ID == "" {
			s.ID = id()
		}
		if !ident.MatchString(s.ID) || used[s.ID] {
			return fmt.Errorf("invalid or duplicate source id")
		}
		used[s.ID] = true
		if s.Decoder != nil {
			d := s.Decoder
			if (d.Kind != "dbc-classical-v1" && d.Kind != "modbus-registers-v1") || len(d.Definition) > 65536 || d.Definition == "" || secretHash(d.Definition) != d.SHA {
				return fmt.Errorf("invalid bounded decoder definition/hash")
			}
		}
		if !validName(s.Name) || !validName(s.Clock) {
			return fmt.Errorf("source name and clock domain required")
		}
		if s.StaleMS == 0 {
			s.StaleMS = 3000
		}
		if s.StaleMS < 10 || s.StaleMS > 86400000 {
			return fmt.Errorf("stale_ms must be 10..86400000")
		}
		if len(s.Events) == 0 || len(s.Events) > 32 {
			return fmt.Errorf("source needs 1..32 event schemas")
		}
		for j := range s.Events {
			sc := &s.Events[j]
			if sc.ID == "" {
				sc.ID = id()
			}
			if !ident.MatchString(sc.ID) || (used[sc.ID] && schemaOwner[sc.ID] != s.ID) {
				return fmt.Errorf("invalid or duplicate schema id")
			}
			used[sc.ID] = true
			schemaOwner[sc.ID] = s.ID
			if sc.Version == 0 {
				sc.Version = 1
			}
			if sc.Version != revisions[sc.ID]+1 || !validName(sc.Name) || len(sc.Fields) == 0 || len(sc.Fields) > 64 {
				return fmt.Errorf("schemas need consecutive immutable versions, a name, and 1..64 fields")
			}
			if s.Decoder != nil {
				found := false
				for _, field := range sc.Fields {
					if field.Name == "decoder_sha256" && field.Type == "string" {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("decoder schemas require string decoder_sha256")
				}
			}
			revisions[sc.ID] = sc.Version
			names := map[string]bool{}
			fieldIDs := map[string]bool{}
			for k := range sc.Fields {
				f := &sc.Fields[k]
				if f.ID == "" {
					f.ID = id()
				}
				definition, _ := json.Marshal(f)
				if !ident.MatchString(f.ID) || (used[f.ID] && (fieldOwner[f.ID] != sc.ID || fieldDefinition[f.ID] != string(definition))) || fieldIDs[f.ID] || !ident.MatchString(f.Name) || names[f.Name] {
					return fmt.Errorf("field names/ids must be unique simple identifiers")
				}
				used[f.ID] = true
				fieldIDs[f.ID] = true
				fieldOwner[f.ID] = sc.ID
				fieldDefinition[f.ID] = string(definition)
				names[f.Name] = true
				switch f.Type {
				case "float64", "int64", "uint64", "bool", "string", "enum":
				default:
					return fmt.Errorf("unsupported type %s", f.Type)
				}
				if len(f.Unit) > 32 || len(f.Enum) > 128 {
					return fmt.Errorf("field metadata too long")
				}
				if f.Type == "enum" && len(f.Enum) == 0 {
					return fmt.Errorf("enum requires labels")
				}
				for _, v := range f.Enum {
					if len(v) > 256 {
						return fmt.Errorf("enum label too long")
					}
				}
			}
		}
	}
	return nil
}
func validateEvent(p Project, e *Event) error {
	var src *Source
	var sc *Schema
	for i := range p.Sources {
		if p.Sources[i].ID == e.Source {
			src = &p.Sources[i]
			break
		}
	}
	if src == nil {
		return fmt.Errorf("unknown source_id")
	}
	if src.Decoder != nil {
		var hash string
		if json.Unmarshal(e.Values["decoder_sha256"], &hash) != nil || hash != src.Decoder.SHA {
			return fmt.Errorf("event decoder hash does not match the declared source mapping")
		}
	}
	for i := range src.Events {
		if src.Events[i].ID == e.Schema && src.Events[i].Version == e.SchemaVersion {
			sc = &src.Events[i]
			break
		}
	}
	if sc == nil || sc.Version != e.SchemaVersion {
		return fmt.Errorf("unknown schema_id/version")
	}
	if !ident.MatchString(e.Boot) {
		return fmt.Errorf("boot_id must be a stable identifier; change on producer restart")
	}
	if !decimal.MatchString(e.Seq) {
		return fmt.Errorf("sequence must be a canonical uint64 decimal string")
	}
	if _, err := strconv.ParseUint(e.Seq, 10, 64); err != nil {
		return fmt.Errorf("sequence overflow")
	}
	if !signed.MatchString(e.Timestamp) {
		return fmt.Errorf("timestamp_ns must be a canonical int64 decimal string")
	}
	if _, err := strconv.ParseInt(e.Timestamp, 10, 64); err != nil {
		return fmt.Errorf("timestamp overflow")
	}
	if e.Clock != src.Clock {
		return fmt.Errorf("clock_domain must match source")
	}
	switch e.Sync {
	case "unknown", "unsynchronized", "synchronized":
	default:
		return fmt.Errorf("sync_quality must be unknown, unsynchronized, or synchronized")
	}
	if e.Uncertainty != nil {
		if !decimal.MatchString(*e.Uncertainty) {
			return fmt.Errorf("uncertainty_ns must be a uint64 string or null")
		}
		if _, err := strconv.ParseUint(*e.Uncertainty, 10, 64); err != nil {
			return err
		}
	}
	if e.Sync == "synchronized" && e.Uncertainty == nil {
		return fmt.Errorf("synchronized clocks require uncertainty_ns")
	}
	if len(e.Values) != len(sc.Fields) {
		return fmt.Errorf("provide all schema fields; use null for unavailable values")
	}
	for _, f := range sc.Fields {
		raw, ok := e.Values[f.Name]
		if !ok {
			return fmt.Errorf("missing field %s", f.Name)
		}
		if string(raw) == "null" {
			continue
		}
		bad := false
		switch f.Type {
		case "float64":
			var v float64
			bad = json.Unmarshal(raw, &v) != nil || math.IsInf(v, 0) || math.IsNaN(v)
		case "int64", "uint64":
			var v string
			bad = json.Unmarshal(raw, &v) != nil
			if !bad {
				if f.Type == "int64" {
					_, err := strconv.ParseInt(v, 10, 64)
					bad = err != nil || !signed.MatchString(v)
				} else {
					_, err := strconv.ParseUint(v, 10, 64)
					bad = err != nil || !decimal.MatchString(v)
				}
			}
		case "bool":
			var v bool
			bad = json.Unmarshal(raw, &v) != nil
		case "string", "enum":
			var v string
			bad = json.Unmarshal(raw, &v) != nil || len(v) > 16384
			if !bad && f.Type == "enum" {
				bad = true
				for _, choice := range f.Enum {
					if v == choice {
						bad = false
					}
				}
			}
		}
		if bad {
			return fmt.Errorf("invalid %s value for %s (int64/uint64 require strings)", f.Type, f.Name)
		}
	}
	if len(e.Message) > 16384 {
		return fmt.Errorf("message too long")
	}
	if e.Level != "" && e.Level != "debug" && e.Level != "info" && e.Level != "warn" && e.Level != "error" {
		return fmt.Errorf("invalid log level")
	}
	encoded, _ := json.Marshal(e)
	if len(encoded) > 64*1024 {
		return fmt.Errorf("event exceeds 64 KiB; split large payloads")
	}
	e.ID = ""
	e.Received = ""
	e.Ordinal = 0
	e.Session = ""
	e.Flags = nil
	return nil
}
