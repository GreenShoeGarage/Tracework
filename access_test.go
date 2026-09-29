// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	pb "tracework/protocol/gen"
)

func TestAccountRolesAndRevocationAcrossLiveTransports(t *testing.T) {
	s, p, ev := fixture(t)
	for _, role := range []string{"viewer", "operator"} {
		if _, e := s.db.Exec("INSERT INTO accounts VALUES(?,?,?,?,?,0,?)", role, role, role, "fixture", "fixture", now()); e != nil {
			t.Fatal(e)
		}
		if _, e := s.db.Exec("INSERT INTO auth_sessions VALUES(?,?,?)", secretHash(role+"-session"), role, time.Now().Add(time.Hour).UnixNano()); e != nil {
			t.Fatal(e)
		}
	}
	a := &API{s: s, slots: make(chan struct{}, 64), streams: make(chan struct{}, 16)}
	httpServer := httptest.NewServer(a.handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ws, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/v1/projects/"+p.ID+"/stream", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.CloseNow()
	wsjson.Write(ctx, ws, map[string]string{"token": "viewer-session"})
	var frame LiveFrame
	if e = wsjson.Read(ctx, ws, &frame); e != nil {
		t.Fatal(e)
	}
	g, l, e := startGRPC(a, "127.0.0.1:0", "", "")
	if e != nil {
		t.Fatal(e)
	}
	defer g.Stop()
	conn, e := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	client := pb.NewAcquisitionClient(conn)
	viewer := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer viewer-session")
	pub, e := client.Publish(viewer)
	if e != nil {
		t.Fatal(e)
	}
	pub.Send(&pb.PublishRequest{})
	if _, e = pub.Recv(); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("viewer published: %v", e)
	}
	sub, e := client.Subscribe(viewer, &pb.SubscribeRequest{ProjectId: p.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sub.Recv(); e != nil {
		t.Fatal(e)
	}
	operator := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer operator-session")
	writer, e := client.Publish(operator)
	if e != nil {
		t.Fatal(e)
	}
	s.start(p.ID, "Account capture")
	b, _ := json.Marshal(EventBatch{Events: []Event{ev}})
	writer.Send(&pb.PublishRequest{ProjectId: p.ID, BatchJson: b})
	if _, e = writer.Recv(); e != nil {
		t.Fatal(e)
	}
	s.db.Exec("UPDATE accounts SET role='viewer' WHERE id='operator'")
	ev.Seq = "1"
	b, _ = json.Marshal(EventBatch{Events: []Event{ev}})
	writer.Send(&pb.PublishRequest{ProjectId: p.ID, BatchJson: b})
	if _, e = writer.Recv(); status.Code(e) != codes.PermissionDenied {
		t.Fatalf("downgraded stream published: %v", e)
	}
	s.db.Exec("UPDATE accounts SET disabled=1 WHERE id='viewer'")
	for i := 0; i < 5; i++ {
		e = wsjson.Read(ctx, ws, &frame)
		if e != nil {
			break
		}
	}
	if websocket.CloseStatus(e) != websocket.StatusPolicyViolation {
		t.Fatalf("revoked WebSocket remained available: %v", e)
	}
	for i := 0; i < 5; i++ {
		_, e = sub.Recv()
		if e != nil {
			break
		}
	}
	if status.Code(e) != codes.Unauthenticated {
		t.Fatalf("revoked gRPC subscription remained available: %v", e)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM records").Scan(&n)
	if n != 1 {
		t.Fatalf("unauthorized batch changed evidence: %d", n)
	}
}
