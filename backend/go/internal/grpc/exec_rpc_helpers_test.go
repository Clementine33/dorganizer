package grpc

import (
	"context"
	pb "github.com/onsei/organizer/backend/internal/gen/onsei/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"os"
	"path/filepath"
	"testing"
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

// createFakeEncoder creates a deterministic fake encoder batch file for tests (Windows-friendly).
// The fake encoder copies src to dst and exits 0, ensuring convert success path is guaranteed.
// lame is called as: lame -b 320 src dst (4 arguments total)
func createFakeEncoder(t *testing.T, tmpDir string) string {
	batchContent := `@echo off
copy /Y %3 %4 >nul 2>&1
exit /b 0
`
	encoderPath := filepath.Join(tmpDir, "fake_lame.bat")
	if err := os.WriteFile(encoderPath, []byte(batchContent), 0755); err != nil {
		t.Fatalf("failed to create fake encoder: %v", err)
	}
	return encoderPath
}
