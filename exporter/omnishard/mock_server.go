// Copyright 2019 OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package omnishard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"

	"github.com/golang/protobuf/ptypes/duration"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	omnishardpb "github.com/signalfx/apm-opentelemetry-collector/exporter/omnishard/gen"
)

type gRPCServer struct {
	srv       *mockServer
	onReceive func(request *omnishardpb.ExportRequest)
	headers   map[string]string
}

func (s *gRPCServer) SetRequiredHeaders(headers map[string]string) {
	s.headers = headers
}

func (s *gRPCServer) Export(ctx context.Context, request *omnishardpb.ExportRequest) (*omnishardpb.ExportResponse, error) {
	if err := s.checkRequiredHeaders(ctx); err != nil {
		return nil, err
	}

	if s.srv.RandomServerError && rand.Float64() < 0.1 {
		status, err := status.New(codes.Unavailable, "Server is unavailable").
			WithDetails(&errdetails.RetryInfo{RetryDelay: &duration.Duration{Nanos: 1e6 * 100}})
		if err != nil {
			log.Fatal(err)
		}
		return nil, status.Err()
	}

	var response *omnishardpb.ExportResponse
	if !shardIsPartOfConfig(request.Shard, s.srv.GetConfig()) {
		// Client's config does not match our config.
		response = &omnishardpb.ExportResponse{
			ResultCode:     omnishardpb.ExportResponse_SHARD_CONFIG_MISTMATCH,
			ShardingConfig: s.srv.GetConfig(),
		}
	} else {
		// Process received request.
		s.onReceive(request)

		// Send response to client.
		response = &omnishardpb.ExportResponse{
			ResultCode:     s.srv.nextResponseCode,
			ShardingConfig: s.srv.nextResponseShardingConfig,
		}
	}

	return response, nil
}

func shardIsPartOfConfig(shard *omnishardpb.ShardDefinition, config *omnishardpb.ShardingConfig) bool {
	for _, s := range config.ShardDefinitions {
		if s.ShardId == shard.ShardId &&
			compareHashKey(s.StartingHashKey, shard.StartingHashKey) == 0 &&
			compareHashKey(s.EndingHashKey, shard.EndingHashKey) == 0 {
			return true
		}
	}
	return false
}

func compareHashKey(k1 []byte, k2 []byte) int {
	if k1 == nil {
		k1 = []byte{}
	}
	if k2 == nil {
		k2 = []byte{}
	}
	return bytes.Compare(k1, k2)
}

func (s *gRPCServer) GetShardingConfig(ctx context.Context, req *omnishardpb.ConfigRequest) (*omnishardpb.ShardingConfig, error) {
	if err := s.checkRequiredHeaders(ctx); err != nil {
		return nil, err
	}

	return s.srv.GetConfig(), nil
}

func (s *gRPCServer) checkRequiredHeaders(ctx context.Context) error {
	if len(s.headers) < 1 {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return errors.New("missing expected headers: no metadata on context")
	}

	for k, v := range s.headers {
		vals := md.Get(k)
		if len(vals) != 1 || vals[0] != v {
			return fmt.Errorf("missing header key %q or incorrect value (want %q) got: %q", k, v, vals)
		}
	}

	return nil
}

type mockServer struct {
	Sink mockServerSink

	RandomServerError bool

	s                          *grpc.Server
	nextResponseCode           omnishardpb.ExportResponse_ResultCode
	nextResponseShardingConfig *omnishardpb.ShardingConfig

	config      *omnishardpb.ShardingConfig
	configMutex sync.Mutex
}

func newMockServer() *mockServer {
	server := &mockServer{
		s:      grpc.NewServer(),
		config: &omnishardpb.ShardingConfig{},
	}
	return server
}

func (srv *mockServer) GetConfig() *omnishardpb.ShardingConfig {
	srv.configMutex.Lock()
	defer srv.configMutex.Unlock()
	return srv.config
}

func (srv *mockServer) SetConfig(config *omnishardpb.ShardingConfig) {
	srv.configMutex.Lock()
	defer srv.configMutex.Unlock()
	srv.config = config
}

func (srv *mockServer) Listen(
	endpoint string,
	onReceive func(request *omnishardpb.ExportRequest),
	headers map[string]string,
) error {
	lis, err := net.Listen("tcp", endpoint)
	if err != nil {
		log.Printf("failed to listen: %v", err)
	}

	grpcSrv := &gRPCServer{srv: srv, onReceive: onReceive}
	if len(headers) > 0 {
		grpcSrv.SetRequiredHeaders(headers)
	}

	omnishardpb.RegisterOmniShardServer(srv.s, grpcSrv)
	if err := srv.s.Serve(lis); err != nil {
		log.Printf("failed to serve: %v", err)
	}

	return nil
}

func (srv *mockServer) Stop() {
	srv.s.Stop()
}

func runServer(srv *mockServer, listenAddress string, headers map[string]string) {
	onReceiveFunc := func(request *omnishardpb.ExportRequest) {
		srv.Sink.onReceive(request)
	}
	srv.Listen(listenAddress, onReceiveFunc, headers)
}

type mockServerSink struct {
	requests []*omnishardpb.ExportRequest
	records  []*omnishardpb.EncodedRecord
	mutex    sync.Mutex
}

func (mss *mockServerSink) onReceive(request *omnishardpb.ExportRequest) {
	mss.mutex.Lock()
	defer mss.mutex.Unlock()
	mss.requests = append(mss.requests, request)
	mss.records = append(mss.records, request.Record)
}

func (mss *mockServerSink) GetRecords() []*omnishardpb.EncodedRecord {
	mss.mutex.Lock()
	defer mss.mutex.Unlock()
	return mss.records
}
