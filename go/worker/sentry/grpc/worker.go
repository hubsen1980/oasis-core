package grpc

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/oasisprotocol/oasis-core/go/common/accessctl"
	"github.com/oasisprotocol/oasis-core/go/common/cbor"
	"github.com/oasisprotocol/oasis-core/go/common/crypto/signature"
	cmnGrpc "github.com/oasisprotocol/oasis-core/go/common/grpc"
	"github.com/oasisprotocol/oasis-core/go/common/grpc/auth"
	"github.com/oasisprotocol/oasis-core/go/common/grpc/policy"
	policyAPI "github.com/oasisprotocol/oasis-core/go/common/grpc/policy/api"
	grpcProxy "github.com/oasisprotocol/oasis-core/go/common/grpc/proxy"
	"github.com/oasisprotocol/oasis-core/go/common/identity"
	"github.com/oasisprotocol/oasis-core/go/common/logging"
	"github.com/oasisprotocol/oasis-core/go/common/service"
)

var _ service.BackgroundService = (*Worker)(nil)

// Worker is a gRPC sentry node worker proxying gRPC requests to upstream node.
type Worker struct { // nolint: maligned
	sync.RWMutex

	enabled bool

	ctx       context.Context
	cancelCtx context.CancelFunc

	initCh chan struct{}
	stopCh chan struct{}
	quitCh chan struct{}

	logger *logging.Logger

	policyWatcher policyAPI.PolicyWatcherClient
	// Per service policy checkers.
	grpcPolicyCheckers map[cmnGrpc.ServiceName]*policy.DynamicRuntimePolicyChecker

	*upstreamConn

	upstreamDialer      grpcProxy.Dialer
	upstreamDialerMutex sync.Mutex

	grpc     *cmnGrpc.Server
	identity *identity.Identity
}

type upstreamConn struct {
	// ID of the upstream node.
	nodeID signature.PublicKey
	// TLS public keys for the upstream node.
	pubKeys []signature.PublicKey
	// Client connection to the upstream node.
	conn *grpc.ClientConn
}

func (g *Worker) authFunction() auth.AuthenticationFunction {
	return func(ctx context.Context,
		fullMethodName string,
		req interface{}) error {

		serviceName := cmnGrpc.ServiceNameFromMethod(fullMethodName)
		if serviceName == "" {
			g.logger.Error("error getting service name from method",
				"method_name", fullMethodName,
			)
			return status.Errorf(codes.PermissionDenied, fmt.Sprintf("invalid service in method: %s", fullMethodName))
		}

		// Get method request type.
		methodDesc, err := cmnGrpc.GetRegisteredMethod(fullMethodName)
		if err != nil {
			g.logger.Error("error getting registered gRPC method",
				"method_name", fullMethodName,
				"err", err,
			)
			return status.Errorf(codes.PermissionDenied, fmt.Sprintf("unknown method: %s", fullMethodName))
		}

		g.RLock()
		defer g.RUnlock()

		// Ensure policy checker for the service exists. This needs to be done
		// before checking if method is access controlled as otherwise the
		// proxy would allow and propagate request for all registered methods
		// without acesss control (even those not implemented by the upstream).
		// XXX: This means that the proxy will reject requests to upstream
		// services that do not provide at least a single policy checker.
		// This is not the case in either of currently supported upstreams
		// (storage and keymanager).
		p, ok := g.grpcPolicyCheckers[serviceName]
		if !ok {
			g.logger.Error("no policy checker defined for service",
				"service_name", serviceName,
				"policy_checkers", g.grpcPolicyCheckers,
			)
			return status.Errorf(codes.PermissionDenied, "not allowed")
		}

		// Proxy defers unmarshalling.
		rawCBOR, ok := req.(*cbor.RawMessage)
		if !ok {
			g.logger.Error("invalid proxy request type, expected *cbor.RawMessage",
				"request", req,
				"request_type", fmt.Sprintf("%T", req),
			)
			return status.Errorf(codes.PermissionDenied, "invalid request")
		}

		// Unmarshal into correct type.
		request, err := methodDesc.UnmarshalRawMessage(rawCBOR)
		if err != nil {
			g.logger.Error("error unamrshaling raw request",
				"err", err,
				"raw", rawCBOR,
			)
			return status.Errorf(codes.PermissionDenied, "invalid request")
		}

		// Check whether access control must be done for this request.
		ac, err := methodDesc.IsAccessControlled(ctx, request)
		if err != nil {
			g.logger.Error("failed to check if request is access controlled",
				"err", err,
			)
			return status.Errorf(codes.PermissionDenied, "internal error")
		}
		if !ac {
			// No access control, allow.
			return nil
		}

		// Extract namespace.
		namespace, err := methodDesc.ExtractNamespace(ctx, request)
		if err != nil {
			g.logger.Error("error extracting namespace from request",
				"err", err,
				"request", request,
			)
			return status.Errorf(codes.PermissionDenied, "invalid request")
		}

		return p.CheckAccessAllowed(ctx, accessctl.Action(fullMethodName), namespace)
	}
}

func (g *Worker) updatePolicies(p policyAPI.ServicePolicies) {
	g.logger.Debug("updating policies",
		"policy", p,
	)

	g.Lock()
	defer g.Unlock()

	g.grpcPolicyCheckers[p.Service] = policy.NewDynamicRuntimePolicyChecker(p.Service, nil)
	for namespace, policy := range p.AccessPolicies {
		g.grpcPolicyCheckers[p.Service].SetAccessPolicy(policy, namespace)
	}
}

func (g *Worker) worker() {
	defer close(g.quitCh)
	defer (g.cancelCtx)()

	if g.upstreamConn == nil {
		dialUpstream := func() error {
			_, err := g.upstreamDialer(g.ctx)
			if err != nil {
				return err
			}
			return nil
		}

		sched := backoff.WithMaxRetries(backoff.NewConstantBackOff(1*time.Second), 60)
		err := backoff.Retry(dialUpstream, backoff.WithContext(sched, g.ctx))
		if err != nil {
			g.logger.Error("unable to dial upstream node",
				"err", err,
			)
			return
		}
	}

	// Initialize policy watcher.
	g.policyWatcher = policyAPI.NewPolicyWatcherClient(g.conn)
	ch, sub, err := g.policyWatcher.WatchPolicies(g.ctx)
	if err != nil {
		g.logger.Error("failed to watch policies",
			"err", err,
		)
		return
	}
	defer sub.Close()

	// Initialization complete.
	close(g.initCh)

	// Watch policies.
	for {
		select {
		case p, ok := <-ch:
			if !ok {
				g.logger.Error("WatchPolicies stream closed")
				return
			}

			g.updatePolicies(p)
		case <-g.stopCh:
			return
		case <-g.grpc.Quit():
			return
		}
	}
}

// Initialized returns a channel that will be closed when the worker initializes.
func (g *Worker) Initialized() <-chan struct{} {
	return g.initCh
}

// Start starts the worker.
func (g *Worker) Start() error {
	if !g.enabled {
		g.logger.Info("not starting gRPC sentry worker as it is disabled")
		return nil
	}

	g.logger.Info("Starting gRPC sentry worker")

	// Start the gRPC sentry server.
	if err := g.grpc.Start(); err != nil {
		g.logger.Error("failed to start external grpc sentry gRPC server",
			"err", err,
		)
		return err
	}

	// Start the worker.
	go g.worker()

	return nil
}

// Name returns the service name.
func (g *Worker) Name() string {
	return "gRPC sentry worker"
}

// Stop halts the worker.
func (g *Worker) Stop() {
	if !g.enabled {
		close(g.quitCh)
		close(g.stopCh)
		return
	}

	g.grpc.Stop()
	close(g.stopCh)
}

// Cleanup performs the service specific post-termination cleanup.
func (g *Worker) Cleanup() {
	if !g.enabled {
		return
	}
	g.grpc.Cleanup()
}

// Quit returns a channel that will be closed when the service terminates.
func (g *Worker) Quit() <-chan struct{} {
	return g.quitCh
}
