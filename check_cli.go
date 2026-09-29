// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func checkCLI(args []string) error {
	f := flag.NewFlagSet("check", flag.ContinueOnError)
	project := f.String("project", "", "project ID")
	session := f.String("session", "", "stopped recording ID")
	rules := f.String("rules", "", "optional checks JSON file; otherwise saved rules")
	junit := f.String("junit", "", "JUnit report path")
	htmlPath := f.String("html", "", "HTML evidence report path")
	if e := f.Parse(args); e != nil {
		return e
	}
	if !ident.MatchString(*project) || !ident.MatchString(*session) {
		return fmt.Errorf("project and session required")
	}
	request := map[string]any{"session_id": *session}
	if *rules != "" {
		b, e := os.ReadFile(*rules)
		if e != nil {
			return e
		}
		if len(b) > 2<<20 {
			return fmt.Errorf("rules file exceeds 2 MiB")
		}
		var doc CheckDocument
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if e = d.Decode(&doc); e != nil {
			return e
		}
		if e = validateChecks(doc); e != nil {
			return e
		}
		request["document"] = doc
	}
	b, _ := json.Marshal(request)
	url := os.Getenv("TRACEWORK_URL")
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	req, e := http.NewRequest("POST", strings.TrimRight(url, "/")+"/api/v1/projects/"+*project+"/check-runs", bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+os.Getenv("TRACEWORK_TOKEN"))
	client := &http.Client{Timeout: 60 * time.Second}
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	b, e = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if e != nil {
		return e
	}
	if resp.StatusCode != 201 {
		return fmt.Errorf("check request failed (%d): %s", resp.StatusCode, b)
	}
	var run CheckRun
	if e = json.Unmarshal(b, &run); e != nil {
		return e
	}
	if *junit != "" {
		if e = os.WriteFile(*junit, checkJUnit(run), 0600); e != nil {
			return e
		}
	}
	if *htmlPath != "" {
		if e = os.WriteFile(*htmlPath, []byte(checkHTML(run)), 0600); e != nil {
			return e
		}
	}
	fmt.Println(string(b))
	if run.Status != "pass" {
		return fmt.Errorf("evidence checks: %s", run.Status)
	}
	return nil
}
