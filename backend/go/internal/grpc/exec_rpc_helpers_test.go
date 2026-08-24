package grpc

import (
	"context"

	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// mockServerStream implements grpc.ServerStreamingServer for testing
type mockServerStream struct {
	ctx    context.Context
	events []*pb.JobEvent
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func (m *mockServerStream) Send(event *pb.JobEvent) error {
	m.events = append(m.events, event)
	return nil
}

// mockServerStreamHelper is a helper that provides the Send method
type mockServerStreamHelper struct {
	events []*pb.JobEvent
	ctx    context.Context
}

func (m *mockServerStreamHelper) Context() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m *mockServerStreamHelper) Send(event *pb.JobEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockServerStreamHelper) SetHeader(md metadata.MD) error {
	return nil
}

func (m *mockServerStreamHelper) SendHeader(md metadata.MD) error {
	return nil
}

func (m *mockServerStreamHelper) SetTrailer(md metadata.MD) {
}

func (m *mockServerStreamHelper) RecvMsg(msg any) error {
	return nil
}

func (m *mockServerStreamHelper) SendMsg(msg any) error {
	return nil
}

// Ensure mockServerStreamHelper implements the interface
var _ grpc.ServerStreamingServer[pb.JobEvent] = (*mockServerStreamHelper)(nil)
