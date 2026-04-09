package envoy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/devopsellence/devopsellence/agent/internal/fileaccess"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"
)

const xdsNodeID = "devopsellence-agent"

type xdsServer struct {
	cache   cachev3.SnapshotCache
	fileGID int
	fileUID int
	logger  *slog.Logger
	mu      sync.Mutex
	grpcSrv *grpc.Server
}

func newXDSServer(logger *slog.Logger, fileUID int, fileGID int) *xdsServer {
	return &xdsServer{
		cache:   cachev3.NewSnapshotCache(false, cachev3.IDHash{}, nil),
		fileGID: fileGID,
		fileUID: fileUID,
		logger:  logger,
	}
}

// Start binds the ADS gRPC server on the given Unix socket path.
// Idempotent: subsequent calls are no-ops if the server is already running.
func (s *xdsServer) Start(ctx context.Context, socketPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.grpcSrv != nil {
		return nil
	}

	if err := fileaccess.EnsureDirOwnershipAndMode(filepath.Dir(socketPath), 0o750, s.fileUID, s.fileGID); err != nil {
		return fmt.Errorf("xds socket dir: %w", err)
	}
	// Remove stale socket from a previous run.
	_ = os.Remove(socketPath)

	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("xds listen %s: %w", socketPath, err)
	}
	// Allow the Envoy container to connect.
	if err := fileaccess.EnsureOwnershipAndMode(socketPath, 0o660, s.fileUID, s.fileGID); err != nil {
		_ = lis.Close()
		return fmt.Errorf("xds socket access: %w", err)
	}

	srv := serverv3.NewServer(ctx, s.cache, nil)
	s.grpcSrv = grpc.NewServer()
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(s.grpcSrv, srv)

	go func() {
		if err := s.grpcSrv.Serve(lis); err != nil {
			s.logger.Error("xds grpc server error", "error", err)
		}
	}()

	s.logger.Info("xds server started", "socket", socketPath)
	return nil
}

// Apply sets the full xDS snapshot for the devopsellence-agent node.
func (s *xdsServer) Apply(snap *cachev3.Snapshot) error {
	return s.cache.SetSnapshot(context.Background(), xdsNodeID, snap)
}
