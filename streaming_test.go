// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	pb "tracework/protocol/gen"
)

func fixture(t *testing.T) (*Store, Project, Event) {
	t.Helper()
	s, e := openStore(t.TempDir(), 32<<20)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.close)
	p, e := s.create(Project{Name: "Custom project", Sources: []Source{{Name: "Observation", Clock: "clock", Events: []Schema{{Name: "sample", Fields: []Field{{Name: "n", Type: "uint64", Unit: "cycles"}}}}}}})
	if e != nil {
		t.Fatal(e)
	}
	ev := Event{Source: p.Sources[0].ID, Schema: p.Sources[0].Events[0].ID, SchemaVersion: 1, Boot: "producer", Seq: "0", Timestamp: "1790612345678901234", Clock: "clock", Sync: "unsynchronized", Values: map[string]json.RawMessage{"n": json.RawMessage(`"18446744073709551615"`)}}
	return s, p, ev
}
func TestWebSocketAuthAndDelivery(t *testing.T) {
	s, p, ev := fixture(t)
	a := &API{s: s, token: "example-access-token-123456789", slots: make(chan struct{}, 64), streams: make(chan struct{}, 16)}
	httpServer := httptest.NewServer(a.handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	open := func() *websocket.Conn {
		c, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http")+"/api/v1/projects/"+p.ID+"/stream", nil)
		if e != nil {
			t.Fatal(e)
		}
		c.SetReadLimit(34 << 20)
		t.Cleanup(func() { c.CloseNow() })
		return c
	}
	wrong := open()
	wsjson.Write(ctx, wrong, map[string]string{"token": "wrong"})
	var f LiveFrame
	if e := wsjson.Read(ctx, wrong, &f); websocket.CloseStatus(e) != websocket.StatusPolicyViolation {
		t.Fatalf("bad token accepted: %v", e)
	}
	c := open()
	wsjson.Write(ctx, c, map[string]string{"token": a.token})
	if e := wsjson.Read(ctx, c, &f); e != nil || !f.Snapshot {
		t.Fatalf("no snapshot %v", e)
	}
	s.start(p.ID, "capture")
	ack, e := s.ingest(p.ID, []Event{ev})
	if e != nil || ack.Persisted != 1 {
		t.Fatal(ack, e)
	}
	if e = wsjson.Read(ctx, c, &f); e != nil || len(f.Events) != 1 || f.Events[0].Timestamp != ev.Timestamp {
		t.Fatalf("bad stream: %+v %v", f, e)
	}
	if string(f.Events[0].Values["n"]) != `"18446744073709551615"` {
		t.Fatal("lost integer precision")
	}
}
func TestStreamEvictionIsExplicit(t *testing.T) {
	s, p, ev := fixture(t)
	for i := 0; i < 2001; i++ {
		ev.Seq = fmt.Sprint(i)
		if _, e := s.ingest(p.ID, []Event{ev}); e != nil {
			t.Fatal(e)
		}
	}
	f, _, e := s.frame(p.ID, 0, false)
	if e != nil || !f.Lost || len(f.Events) != 2000 {
		t.Fatal("missing buffer loss", f.Lost, len(f.Events), e)
	}
}
func TestGRPCBidirectionalPublishSubscribeAndAuth(t *testing.T) {
	s, p, ev := fixture(t)
	a := &API{s: s, token: "test-token", slots: make(chan struct{}, 64), streams: make(chan struct{}, 16)}
	server, l, e := startGRPC(a, "127.0.0.1:0", "", "")
	if e != nil {
		t.Fatal(e)
	}
	defer server.Stop()
	conn, e := grpc.NewClient(l.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	client := pb.NewAcquisitionClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	bad, e := client.Publish(ctx)
	if e != nil {
		t.Fatal(e)
	}
	bad.Send(&pb.PublishRequest{})
	if _, e = bad.Recv(); status.Code(e) != codes.Unauthenticated {
		t.Fatalf("missing auth failure: %v", e)
	}
	auth := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer test-token")
	sub, e := client.Subscribe(auth, &pb.SubscribeRequest{ProjectId: p.ID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = sub.Recv(); e != nil {
		t.Fatal(e)
	}
	publish, e := client.Publish(auth)
	if e != nil {
		t.Fatal(e)
	}
	s.start(p.ID, "grpc capture")
	for i := 0; i < 3; i++ {
		ev.Seq = fmt.Sprint(i)
		b, _ := json.Marshal(EventBatch{Events: []Event{ev}, RequireRecording: true})
		if e = publish.Send(&pb.PublishRequest{ProjectId: p.ID, BatchJson: b}); e != nil {
			t.Fatal(e)
		}
		reply, e := publish.Recv()
		if e != nil {
			t.Fatal(e)
		}
		var ack Ack
		json.Unmarshal(reply.AcknowledgmentJson, &ack)
		if ack.Persisted != 1 {
			t.Fatal("ack not durable", ack)
		}
	}
	total := 0
	for total < 3 {
		reply, e := sub.Recv()
		if e != nil {
			t.Fatal(e)
		}
		var frame LiveFrame
		json.Unmarshal(reply.FrameJson, &frame)
		total += len(frame.Events)
	}
	publish.CloseSend()
}
func TestSchemaMetadataCannotBeReinterpreted(t *testing.T) {
	s, p, ev := fixture(t)
	original := p.Sources[0].Events[0]
	changed := original.Fields[0]
	changed.Unit = "seconds"
	if _, e := s.revise(p.ID, ev.Source, ev.Schema, Revision{1, "sample", []Field{changed}}); e == nil {
		t.Fatal("changed unit retained same field ID")
	}
	changed.ID = ""
	p, e := s.revise(p.ID, ev.Source, ev.Schema, Revision{1, "sample", []Field{changed}})
	if e != nil {
		t.Fatal(e)
	}
	if e = validateEvent(p, &ev); e != nil {
		t.Fatal("old schema stopped accepting", e)
	}
	ev.SchemaVersion = 2
	if e = validateEvent(p, &ev); e != nil {
		t.Fatal(e)
	}
	if _, e = s.revise(p.ID, ev.Source, ev.Schema, Revision{1, "sample", []Field{changed}}); e == nil {
		t.Fatal("stale revision write accepted")
	}
}

func TestGRPCTLSAndRemoteBindGuard(t *testing.T) {
	s, p, ev := fixture(t)
	a := &API{s: s, token: "123456789012345678901234", slots: make(chan struct{}, 64), streams: make(chan struct{}, 16)}
	if g, _, e := startGRPC(a, "0.0.0.0:0", "", ""); e == nil {
		g.Stop()
		t.Fatal("plaintext remote gRPC allowed")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "TRACEWORK test only"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyBytes, _ := x509.MarshalPKCS8PrivateKey(key)
	dir := t.TempDir()
	cf, kf := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	os.WriteFile(cf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(kf, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}), 0600)
	server, l, e := startGRPC(a, "127.0.0.1:0", cf, kf)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Stop()
	sender, e := newGRPCSender(l.Addr().String(), cf, false)
	if e != nil {
		t.Fatal(e)
	}
	defer sender.close()
	t.Setenv("TRACEWORK_TOKEN", a.token)
	s.start(p.ID, "TLS capture")
	b, _ := json.Marshal(EventBatch{Events: []Event{ev}, RequireRecording: true})
	reply, code, e := sender.send(p.ID, b)
	if e != nil || code != 200 {
		t.Fatal(code, e)
	}
	var ack Ack
	json.Unmarshal(reply, &ack)
	if ack.Persisted != 1 {
		t.Fatal(ack)
	}
}
func TestMQTTPacketLimitBeforePayloadAllocation(t *testing.T) {
	for _, tc := range []struct {
		wire []byte
		want int
		bad  bool
	}{{[]byte{0x20, 2, 0, 0, 0xd0, 0}, 6, false}, {[]byte{0x32, 0xff, 0xff, 0x7f}, 0, true}} {
		left, right := net.Pipe()
		go func() { defer right.Close(); right.Write(tc.wire) }()
		conn := &boundedMQTTConn{Conn: left}
		b, e := io.ReadAll(conn)
		left.Close()
		if tc.bad && e == nil {
			t.Fatal("oversized MQTT accepted")
		}
		if !tc.bad && (e != nil || len(b) != tc.want) {
			t.Fatal(b, e)
		}
	}
}
