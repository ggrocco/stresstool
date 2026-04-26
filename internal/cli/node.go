package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"stresstool/internal/auth"
	"stresstool/internal/config"
	"stresstool/internal/placeholders"
	"stresstool/internal/protocol"
	payloadpb "stresstool/internal/protocol/payloadpb/api/v1"
	"stresstool/internal/runner"
	"stresstool/internal/version"
)

const nodeGRPCMaxMsgBytes = 64 << 20

// RunNode connects to the controller via gRPC and runs a worker session.
func RunNode(nodeName, controllerAddr string, verbose bool, tlsOpts TLSOptions) error {
	if nodeName == "" {
		return fmt.Errorf("node name is required")
	}
	if controllerAddr == "" {
		return fmt.Errorf("controller address is required")
	}

	dialOpts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(nodeGRPCMaxMsgBytes),
			grpc.MaxCallSendMsgSize(nodeGRPCMaxMsgBytes),
		),
	}
	tlsConf, err := ClientTLSConfig(tlsOpts)
	if err != nil {
		return err
	}
	if tlsConf != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConf)))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(controllerAddr, dialOpts...)
	if err != nil {
		return fmt.Errorf("connect to controller at %s: %w", controllerAddr, err)
	}
	defer func() { _ = conn.Close() }()

	client := payloadpb.NewStressTestServiceClient(conn)
	stream, err := client.Session(context.Background())
	if err != nil {
		return fmt.Errorf("open session: %w", err)
	}

	ns := &nodeStream{stream: stream}

	if err := ns.Send(&payloadpb.NodeMessage{
		Payload: &payloadpb.NodeMessage_Hello{
			Hello: &payloadpb.HelloMessage{NodeName: nodeName, Version: version.Version},
		},
	}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	fmt.Printf("Connected to controller at %s as node '%s'\n", controllerAddr, nodeName)
	fmt.Println("Waiting for test specifications from controller...")

	n := &grpcWorker{
		nodeName: nodeName,
		verbose:  verbose,
		send:     ns.Send,
	}

	var testMu sync.Mutex
	var testCancel context.CancelFunc

	for {
		in, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			if st, ok := status.FromError(err); ok && st.Code() == codes.Unavailable {
				// Controller process exited or closed the transport without a clean gRPC status.
				return nil
			}
			return fmt.Errorf("receive from controller: %w", err)
		}

		switch p := in.GetPayload().(type) {
		case *payloadpb.ControllerMessage_TestSpec:
			n.handleTestSpec(p.TestSpec)

		case *payloadpb.ControllerMessage_StartTests:
			testMu.Lock()
			if testCancel != nil {
				testCancel()
				testCancel = nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			testCancel = cancel
			testMu.Unlock()

			go func() {
				runErr := n.executeTests(ctx)
				testMu.Lock()
				testCancel = nil
				testMu.Unlock()
				if runErr != nil && verbose {
					fmt.Printf("Test execution error: %v\n", runErr)
				}
			}()

		case *payloadpb.ControllerMessage_StopTests:
			testMu.Lock()
			if testCancel != nil {
				testCancel()
			}
			testMu.Unlock()

		case *payloadpb.ControllerMessage_Complete:
			fmt.Println("All tests complete. Disconnecting...")
			return nil
		}
	}
}

type nodeStream struct {
	mu     sync.Mutex
	stream grpc.BidiStreamingClient[payloadpb.NodeMessage, payloadpb.ControllerMessage]
}

func (ns *nodeStream) Send(m *payloadpb.NodeMessage) error {
	ns.mu.Lock()
	defer ns.mu.Unlock()
	return ns.stream.Send(m)
}

type grpcWorker struct {
	nodeName string
	verbose  bool
	send     func(*payloadpb.NodeMessage) error

	config   *config.Config
	parallel bool
}

func (n *grpcWorker) handleTestSpec(spec *payloadpb.TestSpecMessage) {
	cfg, err := protocol.ConfigFromProto(spec.GetConfig())
	if err != nil {
		fmt.Printf("Failed to parse config: %v\n", err)
		return
	}
	n.config = cfg.WithNodeOverrides(spec.GetNodeName())
	n.parallel = spec.GetParallel()

	if err := n.config.Validate(); err != nil {
		fmt.Printf("Config validation failed: %v\n", err)
		_ = n.send(&payloadpb.NodeMessage{
			Payload: &payloadpb.NodeMessage_Error{
				Error: &payloadpb.ErrorMessage{
					NodeName: n.nodeName,
					Error:    err.Error(),
					Phase:    "validation",
				},
			},
		})
		return
	}

	fmt.Printf("Received test specification with %d test(s)\n", len(n.config.Tests))
	for _, test := range n.config.Tests {
		fmt.Printf("  - %s: %d RPS, %d threads, %ds duration\n",
			test.Name, test.RequestsPerSecond, test.Threads, test.RunSeconds)
	}

	if err := n.send(&payloadpb.NodeMessage{
		Payload: &payloadpb.NodeMessage_Ready{
			Ready: &payloadpb.ReadyMessage{NodeName: n.nodeName},
		},
	}); err != nil {
		fmt.Printf("Failed to send ready message: %v\n", err)
		return
	}

	fmt.Println("Ready to start tests. Waiting for start signal...")
}

func (n *grpcWorker) executeTests(ctx context.Context) error {
	if n.config == nil {
		return fmt.Errorf("no test configuration received")
	}

	fmt.Printf("\nStarting test execution...\n\n")

	eval := placeholders.NewEvaluator(n.config)
	defer eval.Close()

	var authResolver *auth.Resolver
	if n.config.Auth != nil {
		authResolver = auth.NewResolver(n.config.Auth)
		defer authResolver.Close()
	}

	r := runner.NewRunner(eval, n.verbose, authResolver)
	progressChan := make(chan runner.ProgressUpdate, 100)

	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go n.reportProgress(progressChan, &progressWg)

	results := make([]*runner.TestResult, len(n.config.Tests))

	if n.parallel {
		var wg sync.WaitGroup
		for i := range n.config.Tests {
			wg.Add(1)
			test := n.config.Tests[i]
			index := i
			go func(t config.Test, resultIndex int) {
				defer wg.Done()
				results[resultIndex] = r.RunTest(ctx, &t, progressChan)
			}(test, index)
		}
		wg.Wait()
	} else {
		for i := range n.config.Tests {
			test := n.config.Tests[i]
			results[i] = r.RunTest(ctx, &test, progressChan)
		}
	}

	close(progressChan)
	progressWg.Wait()

	for _, result := range results {
		if result == nil || result.Test == nil {
			continue
		}
		msg := &payloadpb.NodeMessage{
			Payload: &payloadpb.NodeMessage_TestResult{
				TestResult: &payloadpb.TestResultMessage{
					NodeName: n.nodeName,
					TestName: result.Test.Name,
					Result:   protocol.TestResultToProto(result),
				},
			},
		}
		if err := n.sendWithRetry(msg); err != nil {
			return fmt.Errorf("send test result for %s: %w", result.Test.Name, err)
		}
	}

	fmt.Println("\n✓ All tests completed. Results sent to controller.")
	return nil
}

func (n *grpcWorker) sendWithRetry(msg *payloadpb.NodeMessage) error {
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := n.send(msg); err != nil {
			lastErr = err
			if attempt < maxRetries-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
		} else {
			return nil
		}
	}
	return lastErr
}

func (n *grpcWorker) reportProgress(progressChan <-chan runner.ProgressUpdate, wg *sync.WaitGroup) {
	defer wg.Done()
	for update := range progressChan {
		msg := &payloadpb.NodeMessage{
			Payload: &payloadpb.NodeMessage_Progress{
				Progress: &payloadpb.ProgressMessage{
					NodeName:       n.nodeName,
					TestName:       update.TestName,
					ElapsedSeconds: update.Elapsed.Seconds(),
					TotalRequests:  update.Total,
					Failures:       update.Failures,
					Rps:            update.RPS,
					Done:           update.Done,
				},
			},
		}
		if err := n.send(msg); err != nil && n.verbose {
			fmt.Printf("Failed to send progress update: %v\n", err)
		}
		if n.verbose {
			if update.Done {
				fmt.Printf("✓ %s: COMPLETE - %d requests, %.1f RPS, %d failures\n",
					update.TestName, update.Total, update.RPS, update.Failures)
			} else {
				fmt.Printf("→ %s: %.0fs elapsed - %d requests, %.1f RPS, %d failures\n",
					update.TestName, update.Elapsed.Seconds(), update.Total, update.RPS, update.Failures)
			}
		}
	}
}
