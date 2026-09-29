// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"
)

func (a *API) example(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	name, field, unit := "Simulated motor fault", "speed", "rpm"
	if kind == "serial" {
		name, field, unit = "Serial sensor fixture", "temperature", "°C"
	} else if kind == "service" {
		name, field, unit = "Software service fixture", "latency", "ms"
	} else if kind != "motor" {
		fail(w, 400, fmt.Errorf("unknown example"))
		return
	}
	p, e := a.s.create(Project{Name: name, Description: "Synthetic teaching fixture. No physical equipment connected.", Sample: true, Sources: []Source{{ID: id(), Name: kind + "-fixture", Clock: "fixture-clock", Synthetic: true, StaleMS: 3000, Events: []Schema{{ID: id(), Name: "measurement", Version: 1, Fields: []Field{{ID: id(), Name: field, Type: "float64", Unit: unit}, {ID: id(), Name: "status", Type: "enum", Enum: []string{"normal", "fault"}}}}}}}})
	if e != nil {
		fail(w, 500, e)
		return
	}
	x, e := a.s.start(p.ID, "Synthetic example · "+kind)
	if e != nil {
		fail(w, 500, e)
		return
	}
	events := []Event{}
	t := time.Now().Add(-24 * time.Second).UnixNano()
	src := p.Sources[0]
	boot := id()
	for i := 0; i < 240; i++ {
		v := 1200 + 60*math.Sin(float64(i)/13)
		state := "normal"
		if i > 130 && i < 170 {
			v -= 600
			state = "fault"
		}
		if kind == "serial" {
			v = 22 + 2*math.Sin(float64(i)/25)
		}
		if kind == "service" {
			v = 30 + 5*math.Sin(float64(i)/7)
			if i > 130 && i < 170 {
				v += 180
			}
		}
		raw, _ := json.Marshal(v)
		sr, _ := json.Marshal(state)
		ev := Event{Source: src.ID, Schema: src.Events[0].ID, SchemaVersion: 1, Boot: boot, Seq: strconv.Itoa(i), Timestamp: strconv.FormatInt(t+int64(i)*100000000, 10), Clock: src.Clock, Sync: "unsynchronized", Values: map[string]json.RawMessage{field: raw, "status": sr}}
		if i%40 == 0 {
			ev.Message = "Synthetic fixture checkpoint"
			ev.Level = "info"
		}
		if i == 131 {
			ev.Message = "Synthetic fault begins"
			ev.Level = "warn"
		}
		if i == 100 {
			ev.Gap = true
		}
		events = append(events, ev)
	}
	if _, e = a.s.ingest(p.ID, events); e != nil {
		fail(w, 500, e)
		return
	}
	if e = a.s.stop(x.ID); e != nil {
		fail(w, 500, e)
		return
	}
	respond(w, 201, map[string]any{"project": p, "session_id": x.ID})
}
