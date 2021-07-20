package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/oasisprotocol/oasis-core/go/common"
	"github.com/oasisprotocol/oasis-core/go/common/grpc/auth"
	"github.com/oasisprotocol/oasis-core/go/common/grpc/policy"
	registry "github.com/oasisprotocol/oasis-core/go/registry/api"
	"github.com/oasisprotocol/oasis-core/go/runtime/transaction"
	"github.com/oasisprotocol/oasis-core/go/storage/api"
	"github.com/oasisprotocol/oasis-core/go/storage/mkvs/checkpoint"
)

var (
	_ api.Backend     = (*storageService)(nil)
	_ auth.ServerAuth = (*storageService)(nil)

	errDebugRejectUpdates = errors.New("storage: (debug) rejecting update operations")
)

// storageService is the service exposed to external clients via gRPC.
type storageService struct {
	w       *Worker
	storage api.Backend

	debugRejectUpdates bool
}

func (s *storageService) AuthFunc(ctx context.Context, fullMethodName string, req interface{}) error {
	return policy.GRPCAuthenticationFunction(s.w.grpcPolicy)(ctx, fullMethodName, req)
}

func (s *storageService) ensureInitialized(ctx context.Context) error {
	select {
	case <-s.Initialized():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *storageService) getConfig(ctx context.Context, ns common.Namespace) (*registry.Runtime, error) {
	rt, err := s.w.commonWorker.RuntimeRegistry.GetRuntime(ns)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to get runtime %s: %w", ns, err)
	}

	rtDesc, err := rt.ActiveDescriptor(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage: failed to get runtime %s configuration: %w", ns, err)
	}
	return rtDesc, nil
}

func (s *storageService) SyncGet(ctx context.Context, request *api.GetRequest) (*api.ProofResponse, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.storage.SyncGet(ctx, request)
}

func (s *storageService) SyncGetPrefixes(ctx context.Context, request *api.GetPrefixesRequest) (*api.ProofResponse, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.storage.SyncGetPrefixes(ctx, request)
}

func (s *storageService) SyncIterate(ctx context.Context, request *api.IterateRequest) (*api.ProofResponse, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.storage.SyncIterate(ctx, request)
}

func (s *storageService) Apply(ctx context.Context, request *api.ApplyRequest) ([]*api.Receipt, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	if s.debugRejectUpdates {
		return nil, errDebugRejectUpdates
	}

	// Limit maximum number of entries in a write log.
	cfg, err := s.getConfig(ctx, request.Namespace)
	if err != nil {
		return nil, err
	}
	if uint64(len(request.WriteLog)) > cfg.Storage.MaxApplyWriteLogEntries {
		return nil, api.ErrLimitReached
	}

	// Validate the write log for IO roots.
	if request.RootType == api.RootTypeIO {
		err := transaction.ValidateIOWriteLog(
			request.WriteLog,
			cfg.TxnScheduler.MaxBatchSize,
			cfg.TxnScheduler.MaxBatchSizeBytes,
		)
		if err != nil {
			return nil, fmt.Errorf("storage: malformed io root in Apply: %w", err)
		}
	}

	return s.storage.Apply(ctx, request)
}

func (s *storageService) ApplyBatch(ctx context.Context, request *api.ApplyBatchRequest) ([]*api.Receipt, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	if s.debugRejectUpdates {
		return nil, errDebugRejectUpdates
	}

	// Limit maximum number of operations in a batch.
	cfg, err := s.getConfig(ctx, request.Namespace)
	if err != nil {
		return nil, err
	}
	if uint64(len(request.Ops)) > cfg.Storage.MaxApplyOps {
		return nil, api.ErrLimitReached
	}
	// Limit maximum number of entries in a write log and validate write logs for IO roots.
	for _, op := range request.Ops {
		if uint64(len(op.WriteLog)) > cfg.Storage.MaxApplyWriteLogEntries {
			return nil, api.ErrLimitReached
		}
		if op.RootType == api.RootTypeIO {
			err := transaction.ValidateIOWriteLog(
				op.WriteLog,
				cfg.TxnScheduler.MaxBatchSize,
				cfg.TxnScheduler.MaxBatchSizeBytes,
			)
			if err != nil {
				return nil, fmt.Errorf("storage: malformed io root in ApplyBatch: %w", err)
			}
		}
	}

	return s.storage.ApplyBatch(ctx, request)
}

func (s *storageService) GetDiff(ctx context.Context, request *api.GetDiffRequest) (api.WriteLogIterator, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.storage.GetDiff(ctx, request)
}

func (s *storageService) GetCheckpoints(ctx context.Context, request *checkpoint.GetCheckpointsRequest) ([]*checkpoint.Metadata, error) {
	if err := s.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	return s.storage.GetCheckpoints(ctx, request)
}

func (s *storageService) GetCheckpointChunk(ctx context.Context, chunk *checkpoint.ChunkMetadata, w io.Writer) error {
	if err := s.ensureInitialized(ctx); err != nil {
		return err
	}
	return s.storage.GetCheckpointChunk(ctx, chunk, w)
}

func (s *storageService) Cleanup() {
}

func (s *storageService) Initialized() <-chan struct{} {
	return s.storage.Initialized()
}
