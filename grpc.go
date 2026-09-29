// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	pb "tracework/protocol/gen"
)

type acquisition struct {
	pb.UnimplementedAcquisitionServer
	api *API
}

func (g *acquisition) Publish(stream grpc.BidiStreamingServer[pb.PublishRequest, pb.PublishReply]) error {
	for {
		req, e := stream.Recv()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		actor, authErr := g.api.grpcPrincipal(stream.Context(), true)
		if authErr != nil {
			return authErr
		}
		var b EventBatch
		d := json.NewDecoder(bytes.NewReader(req.BatchJson))
		d.DisallowUnknownFields()
		if e = d.Decode(&b); e != nil {
			return status.Error(codes.InvalidArgument, e.Error())
		}
		if d.Decode(new(any)) != io.EOF {
			return status.Error(codes.InvalidArgument, "expected one JSON value")
		}
		requestID := id()
		if e = g.api.s.auditAccess(actor.Name+" ("+actor.ID+")", "gRPC Publish", req.ProjectId, 0, requestID); e != nil {
			return status.Error(codes.Unavailable, "audit unavailable")
		}
		ack, e := g.api.s.ingestBatch(req.ProjectId, b)
		if e != nil {
			if errors.Is(e, errRecordingRequired) {
				return status.Error(codes.FailedPrecondition, e.Error())
			}
			return status.Error(codes.InvalidArgument, e.Error())
		}
		if err := g.api.s.auditAccess(actor.Name+" ("+actor.ID+")", "gRPC Publish", req.ProjectId, 200, requestID); err != nil {
			return status.Error(codes.Unavailable, "batch acknowledged internally; audit completion unavailable; retry the same events")
		}
		data, _ := json.Marshal(ack)
		if e = stream.Send(&pb.PublishReply{AcknowledgmentJson: data}); e != nil {
			return e
		}
	}
}
func (g *acquisition) Subscribe(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.LiveReply]) error {
	select {
	case g.api.streams <- struct{}{}:
		defer func() { <-g.api.streams }()
	default:
		return status.Error(codes.ResourceExhausted, "maximum 16 live subscribers")
	}
	after := int64(0)
	initial := true
	for {
		if _, err := g.api.grpcPrincipal(stream.Context(), false); err != nil {
			return err
		}
		f, changed, e := g.api.s.frame(req.ProjectId, after, initial)
		if e != nil {
			return status.Error(codes.NotFound, "project unavailable")
		}
		b, _ := json.Marshal(f)
		if e = stream.Send(&pb.LiveReply{FrameJson: b}); e != nil {
			return e
		}
		after = f.Next
		initial = false
		timer := time.NewTimer(time.Second)
		select {
		case <-stream.Context().Done():
			timer.Stop()
			return stream.Context().Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func startGRPC(a *API, address, cert, key string) (*grpc.Server, net.Listener, error) {
	host, _, e := net.SplitHostPort(address)
	if e != nil {
		return nil, nil, e
	}
	ip := net.ParseIP(host)
	loop := ip != nil && ip.IsLoopback()
	if !loop && (len(a.token) < 24 || cert == "" || key == "") {
		return nil, nil, fmt.Errorf("remote gRPC requires a 24-character TRACEWORK_TOKEN and --grpc-cert / --grpc-key TLS files")
	}
	if (cert == "") != (key == "") {
		return nil, nil, fmt.Errorf("provide both gRPC TLS files")
	}
	opts := []grpc.ServerOption{grpc.MaxRecvMsgSize(4 << 20), grpc.MaxSendMsgSize(34 << 20), grpc.MaxConcurrentStreams(16), grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if _, err := a.grpcPrincipal(stream.Context(), strings.HasSuffix(info.FullMethod, "/Publish")); err != nil {
			return err
		}
		select {
		case a.slots <- struct{}{}:
			defer func() { <-a.slots }()
		default:
			return status.Error(codes.ResourceExhausted, "server busy")
		}
		return handler(srv, stream)
	})}
	if cert != "" {
		creds, e := credentials.NewServerTLSFromFile(cert, key)
		if e != nil {
			return nil, nil, e
		}
		opts = append(opts, grpc.Creds(creds))
	}
	l, e := net.Listen("tcp", address)
	if e != nil {
		return nil, nil, e
	}
	server := grpc.NewServer(opts...)
	pb.RegisterAcquisitionServer(server, &acquisition{api: a})
	a.grpcAddress = address
	go server.Serve(l)
	return server, l, nil
}

type grpcSender struct {
	conn   *grpc.ClientConn
	stream grpc.BidiStreamingClient[pb.PublishRequest, pb.PublishReply]
	cancel context.CancelFunc
}

func newGRPCSender(address, ca string, plaintext bool) (*grpcSender, error) {
	var creds credentials.TransportCredentials
	if plaintext {
		host, _, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return nil, fmt.Errorf("plaintext gRPC is limited to numeric loopback addresses")
		}
		creds = insecure.NewCredentials()
	} else {
		cfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if ca != "" {
			b, e := os.ReadFile(ca)
			if e != nil {
				return nil, e
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(b) {
				return nil, fmt.Errorf("invalid gRPC CA file")
			}
			cfg.RootCAs = pool
		}
		creds = credentials.NewTLS(cfg)
	}
	conn, e := grpc.NewClient(address, grpc.WithTransportCredentials(creds), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20)))
	return &grpcSender{conn: conn}, e
}
func (g *grpcSender) send(pid string, b []byte) ([]byte, int, error) {
	if g.stream == nil {
		ctx, cancel := context.WithCancel(context.Background())
		g.cancel = cancel
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+os.Getenv("TRACEWORK_TOKEN"))
		stream, e := pb.NewAcquisitionClient(g.conn).Publish(ctx)
		if e != nil {
			cancel()
			return nil, 0, e
		}
		g.stream = stream
	}
	timer := time.AfterFunc(30*time.Second, g.cancel)
	defer timer.Stop()
	e := g.stream.Send(&pb.PublishRequest{ProjectId: pid, BatchJson: b})
	var reply *pb.PublishReply
	if e == nil {
		reply, e = g.stream.Recv()
	}
	if e != nil {
		g.cancel()
		g.stream = nil
		code := 0
		switch status.Code(e) {
		case codes.FailedPrecondition:
			code = 409
		case codes.InvalidArgument:
			code = 422
		case codes.Unauthenticated:
			code = 401
		case codes.ResourceExhausted:
			code = 429
		}
		return nil, code, e
	}
	return reply.AcknowledgmentJson, 200, nil
}
func (g *grpcSender) close() {
	if g.cancel != nil {
		g.cancel()
	}
	g.conn.Close()
}

func (a *API) grpcPrincipal(ctx context.Context, write bool) (Principal, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	tokens := md.Get("authorization")
	token := ""
	if len(tokens) == 1 {
		token = bearerToken(tokens[0])
	}
	p, e := a.authenticate(token)
	if e != nil {
		return p, status.Error(codes.Unauthenticated, "credential required")
	}
	if write && roleRank(p.Role) < 2 {
		return p, status.Error(codes.PermissionDenied, "operator role required")
	}
	return p, nil
}
