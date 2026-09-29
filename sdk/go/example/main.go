// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	tw "tracework/sdk/go/tracework"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	base := os.Getenv("TRACEWORK_URL")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	c, e := tw.New(base, os.Getenv("TRACEWORK_TOKEN"))
	if e != nil {
		return e
	}
	ctx := context.Background()
	p, e := c.CreateProject(ctx, "Go SDK conformance")
	if e != nil {
		return e
	}
	src, e := c.DefineSource(ctx, p.ID, tw.SourceDefinition{Name: "Counter", Clock: "go-clock", StaleMS: 3000, Events: []tw.Schema{{Name: "sample", Version: 1, Fields: []tw.Field{{Name: "count", Type: "uint64", Unit: "cycles"}}}}})
	if e != nil {
		return e
	}
	record, e := c.StartRecording(ctx, p.ID, "Go native exact evidence")
	if e != nil {
		return e
	}
	source := c.Source(p.ID, src, src.Events[0])
	source.Recording = record.ID
	if e = source.Emit(ctx, map[string]json.RawMessage{"count": tw.ExactUint64(18446744073709551615)}, 1790612345678901234, false); e != nil {
		return e
	}
	if e = c.StopRecording(ctx, record.ID); e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"project_id": p.ID, "session_id": record.ID})
}
