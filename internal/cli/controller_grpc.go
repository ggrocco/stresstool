package cli

import (
	"fmt"
	"io"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"stresstool/internal/protocol"
	payloadpb "stresstool/internal/protocol/payloadpb/api/v1"
)

const grpcMaxMsgSize = 64 << 20 // 64 MiB for large latency histograms

type grpcStressService struct {
	payloadpb.UnimplementedStressTestServiceServer
	c *Controller
}

func (g *grpcStressService) Session(stream grpc.BidiStreamingServer[payloadpb.NodeMessage, payloadpb.ControllerMessage]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be hello")
	}

	peerAddr := ""
	if p, ok := peer.FromContext(stream.Context()); ok {
		peerAddr = p.Addr.String()
	}

	sendCh := make(chan *payloadpb.ControllerMessage, 64)
	sendErr := make(chan error, 1)
	go func() {
		for msg := range sendCh {
			if err := stream.Send(msg); err != nil {
				sendErr <- err
				return
			}
		}
		sendErr <- nil
	}()

	nodeName := hello.GetNodeName()
	nc := &NodeConnection{
		Name:     nodeName,
		PeerAddr: peerAddr,
		SendCh:   sendCh,
	}

	g.c.eventChan <- controllerEvent{
		kind:     evNodeConnected,
		nodeName: nodeName,
		conn:     nc,
	}

	defer func() {
		close(sendCh)
		<-sendErr
		g.c.eventChan <- controllerEvent{kind: evNodeDisconnected, nodeName: nodeName}
	}()

	for {
		in, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch p := in.GetPayload().(type) {
		case *payloadpb.NodeMessage_Ready:
			g.c.eventChan <- controllerEvent{kind: evNodeReady, nodeName: p.Ready.GetNodeName()}
		case *payloadpb.NodeMessage_Progress:
			pr := p.Progress
			g.c.eventChan <- controllerEvent{
				kind: evProgress,
				progress: &protocol.ProgressMessage{
					NodeName: pr.GetNodeName(),
					TestName: pr.GetTestName(),
					Elapsed:  pr.GetElapsedSeconds(),
					Total:    pr.GetTotalRequests(),
					Failures: pr.GetFailures(),
					RPS:      pr.GetRps(),
					Done:     pr.GetDone(),
				},
			}
		case *payloadpb.NodeMessage_TestResult:
			tr := p.TestResult
			res, convErr := protocol.TestResultFromProto(tr.GetResult())
			if convErr != nil {
				fmt.Printf("Failed to decode test result from %s: %v\n", nodeName, convErr)
				continue
			}
			g.c.eventChan <- controllerEvent{
				kind: evTestResult,
				result: &protocol.TestResultMessage{
					NodeName: tr.GetNodeName(),
					TestName: tr.GetTestName(),
					Result:   res,
				},
			}
		case *payloadpb.NodeMessage_Error:
			em := p.Error
			g.c.eventChan <- controllerEvent{
				kind: evNodeError,
				nodeErr: &protocol.ErrorMessage{
					NodeName: em.GetNodeName(),
					Error:    em.GetError(),
					Phase:    em.GetPhase(),
				},
			}
		case *payloadpb.NodeMessage_Hello:
			// Ignore duplicate hellos
		default:
		}
	}
}

func listenAndServeGRPC(c *Controller, tlsOpts TLSOptions) error {
	lis, err := net.Listen("tcp", c.listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", c.listenAddr, err)
	}

	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(grpcMaxMsgSize),
		grpc.MaxSendMsgSize(grpcMaxMsgSize),
	}
	tlsConf, err := ServerTLSConfig(tlsOpts)
	if err != nil {
		return err
	}
	if tlsConf != nil {
		opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConf)))
	}

	srv := grpc.NewServer(opts...)
	payloadpb.RegisterStressTestServiceServer(srv, &grpcStressService{c: c})

	go func() {
		if err := srv.Serve(lis); err != nil {
			fmt.Printf("gRPC server error: %v\n", err)
		}
	}()

	return c.runControllerLoop()
}
