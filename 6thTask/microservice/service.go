package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type microservice struct {
	acl         map[string][]string
	logMutex    sync.Mutex
	logStreams  map[uint64]chan *Event
	nextLogID   uint64
	statMutex   sync.Mutex
	statStreams map[uint64]*statStream
	nextStatID  uint64
}

type statStream struct {
	intervalSeconds uint64
	byMethod        map[string]uint64
	byConsumer      map[string]uint64
}

type adminServerImpl struct {
	UnimplementedAdminServer
	service *microservice
}

type bizServerImpl struct {
	UnimplementedBizServer
}

func (service *microservice) checkACL(ctx context.Context, method string) (string, error) {
	metaData, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "no metadata")
	}
	consumers := metaData.Get("consumer")
	if len(consumers) == 0 {
		return "", status.Error(codes.Unauthenticated, "no consumer")
	}
	consumer := consumers[0]
	allowed, ok := service.acl[consumer]
	if !ok {
		return "", status.Error(codes.Unauthenticated, "unknown consumer")
	}
	for _, pattern := range allowed {
		if pattern == method {
			return consumer, nil
		}
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(method, prefix) {
				return consumer, nil
			}
		}
	}
	return "", status.Error(codes.Unauthenticated, "method not allowed")
}

func (service *microservice) unaryInterceptor(
	ctx context.Context,
	request interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	consumer, err := service.checkACL(ctx, info.FullMethod)
	if err != nil {
		return nil, err
	}
	service.logEvent(ctx, consumer, info.FullMethod)
	service.incrementStat(consumer, info.FullMethod)
	return handler(ctx, request)
}

func (service *microservice) streamInterceptor(
	srv interface{},
	serverStream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	ctx := serverStream.Context()
	consumer, err := service.checkACL(ctx, info.FullMethod)
	if err != nil {
		return err
	}
	service.logEvent(ctx, consumer, info.FullMethod)
	service.incrementStat(consumer, info.FullMethod)
	return handler(srv, serverStream)
}

func (service *microservice) logEvent(ctx context.Context, consumer, method string) {
	host := ""
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		host = p.Addr.String()
	}
	evt := &Event{Consumer: consumer, Method: method, Host: host}
	service.logMutex.Lock()
	defer service.logMutex.Unlock()
	for _, ch := range service.logStreams {
		select {
		case ch <- evt:
		default:
		}
	}
}

func (service *microservice) incrementStat(consumer, method string) {
	service.statMutex.Lock()
	for _, statSteam := range service.statStreams {
		statSteam.byMethod[method]++
		statSteam.byConsumer[consumer]++
	}
	service.statMutex.Unlock()
}

func parseACL(s string) (map[string][]string, error) {
	var acl map[string][]string
	if err := json.Unmarshal([]byte(s), &acl); err != nil {
		return nil, err
	}
	return acl, nil
}

func (admin *adminServerImpl) Logging(
	_ *Nothing,
	stream grpc.ServerStreamingServer[Event],
) error {
	ch := make(chan *Event, 100)
	admin.service.logMutex.Lock()
	id := admin.service.nextLogID
	admin.service.nextLogID++
	admin.service.logStreams[id] = ch
	admin.service.logMutex.Unlock()
	defer func() {
		admin.service.logMutex.Lock()
		delete(admin.service.logStreams, id)
		admin.service.logMutex.Unlock()
	}()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case evt := <-ch:
			if err := stream.Send(evt); err != nil {
				return err
			}
		}
	}
}

func (admin *adminServerImpl) Statistics(
	in *StatInterval,
	stream grpc.ServerStreamingServer[Stat],
) error {
	if in.IntervalSeconds == 0 {
		return status.Error(codes.InvalidArgument, "interval must be > 0")
	}

	stat := &statStream{
		byMethod:   make(map[string]uint64),
		byConsumer: make(map[string]uint64),
	}

	admin.service.statMutex.Lock()
	id := admin.service.nextStatID
	admin.service.nextStatID++
	admin.service.statStreams[id] = stat
	admin.service.statMutex.Unlock()

	defer func() {
		admin.service.statMutex.Lock()
		delete(admin.service.statStreams, id)
		admin.service.statMutex.Unlock()
	}()

	ticker := time.NewTicker(time.Duration(in.IntervalSeconds) * time.Second)

	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			admin.service.statMutex.Lock()
			st := &Stat{
				ByMethod:   stat.byMethod,
				ByConsumer: stat.byConsumer,
			}
			stat.byMethod = make(map[string]uint64)
			stat.byConsumer = make(map[string]uint64)
			admin.service.statMutex.Unlock()
			if err := stream.Send(st); err != nil {
				return err
			}
		}
	}
}

func (b *bizServerImpl) Check(ctx context.Context, _ *Nothing) (*Nothing, error) {
	return &Nothing{}, nil
}

func (b *bizServerImpl) Add(ctx context.Context, _ *Nothing) (*Nothing, error) {
	return &Nothing{}, nil
}

func (b *bizServerImpl) Test(ctx context.Context, _ *Nothing) (*Nothing, error) {
	return &Nothing{}, nil
}

func StartMyMicroservice(ctx context.Context, addr string, acl string) error {
	parsedACL, err := parseACL(acl)
	if err != nil {
		return err
	}
	svc := &microservice{
		acl:         parsedACL,
		logStreams:  make(map[uint64]chan *Event),
		statStreams: make(map[uint64]*statStream),
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(svc.unaryInterceptor),
		grpc.StreamInterceptor(svc.streamInterceptor),
	)
	RegisterAdminServer(grpcServer, &adminServerImpl{service: svc})
	RegisterBizServer(grpcServer, &bizServerImpl{})
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	go func() {
		<-ctx.Done()
		grpcServer.Stop()
	}()
	return nil
}
