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

package opencensusreceiver

import (
	"fmt"
	"time"

	"github.com/open-telemetry/opentelemetry-collector/config/configmodels"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/signalfx/apm-opentelemetry-collector/receiver/opencensusreceiver/octrace"
)

// Config defines configuration for OpenCensus receiver.
type Config struct {
	configmodels.ReceiverSettings `mapstructure:",squash"` // squash ensures fields are correctly decoded in embedded struct

	// TLSCredentials is a (cert_file, key_file) configuration.
	TLSCredentials *tlsCredentials `mapstructure:"tls_credentials"`

	// Keepalive anchor for all the settings related to keepalive.
	Keepalive *serverParametersAndEnforcementPolicy `mapstructure:"keepalive"`

	// MaxRecvMsgSizeMiB sets the maximum size (in MiB) of messages accepted by the server.
	MaxRecvMsgSizeMiB uint64 `mapstructure:"max_recv_msg_size_mib"`

	// MaxConcurrentStreams sets the limit on the number of concurrent streams to each ServerTransport.
	MaxConcurrentStreams uint32 `mapstructure:"max_concurrent_streams"`

	// EnableBackPressure indicates if the server should put back-pressure on callers (by
	// dropping connections) or not,
	EnableBackPressure bool `mapstructure:"backpressure"`

	// MaxServerStreams sets the limit on the number of receiving routines for the trace receiver.
	MaxServerStreams uint64 `mapstructure:"max_server_streams"`
}

// tlsCredentials holds the fields for TLS credentials
// that are used for starting a server.
type tlsCredentials struct {
	// CertFile is the file path containing the TLS certificate.
	CertFile string `mapstructure:"cert_file"`

	// KeyFile is the file path containing the TLS key.
	KeyFile string `mapstructure:"key_file"`
}

type serverParametersAndEnforcementPolicy struct {
	ServerParameters  *keepaliveServerParameters  `mapstructure:"server_parameters"`
	EnforcementPolicy *keepaliveEnforcementPolicy `mapstructure:"enforcement_policy"`
}

// keepaliveServerParameters allow configuration of the keepalive.ServerParameters.
// See https://godoc.org/google.golang.org/grpc/keepalive#ServerParameters for details.
type keepaliveServerParameters struct {
	MaxConnectionIdle     time.Duration `mapstructure:"max_connection_idle"`
	MaxConnectionAge      time.Duration `mapstructure:"max_connection_age"`
	MaxConnectionAgeGrace time.Duration `mapstructure:"max_connection_age_grace"`
	Time                  time.Duration `mapstructure:"time"`
	Timeout               time.Duration `mapstructure:"timeout"`
}

// keepaliveEnforcementPolicy allow configuration of the keepalive.EnforcementPolicy.
// See https://godoc.org/google.golang.org/grpc/keepalive#EnforcementPolicy for details.
type keepaliveEnforcementPolicy struct {
	MinTime             time.Duration `mapstructure:"min_time"`
	PermitWithoutStream bool          `mapstructure:"permit_without_stream"`
}

func (rOpts *Config) buildOptions() (opts []Option, err error) {
	tlsCredsOption, hasTLSCreds, err := rOpts.TLSCredentials.ToOpenCensusReceiverServerOption()
	if err != nil {
		return opts, fmt.Errorf("OpenCensus receiver TLS Credentials: %v", err)
	}
	if hasTLSCreds {
		opts = append(opts, tlsCredsOption)
	}

	grpcServerOptions := rOpts.grpcServerOptions()
	if len(grpcServerOptions) > 0 {
		opts = append(opts, WithGRPCServerOptions(grpcServerOptions...))
	}

	traceReceiverOptions := rOpts.traceReceiverOptions()
	if len(traceReceiverOptions) > 0 {
		opts = append(opts, WithTraceReceiverOptions(traceReceiverOptions...))
	}

	return opts, err
}

func (rOpts *Config) traceReceiverOptions() []octrace.Option {
	var opts []octrace.Option

	if rOpts.EnableBackPressure {
		opts = append(opts, octrace.WithBackPressure())
	}

	if rOpts.MaxServerStreams > 0 {
		opts = append(opts, octrace.WithMaxServerStream(int64(rOpts.MaxServerStreams)))
	}
	return opts
}

func (rOpts *Config) grpcServerOptions() []grpc.ServerOption {
	var grpcServerOptions []grpc.ServerOption
	if rOpts.MaxRecvMsgSizeMiB > 0 {
		grpcServerOptions = append(grpcServerOptions, grpc.MaxRecvMsgSize(int(rOpts.MaxRecvMsgSizeMiB*1024*1024)))
	}
	if rOpts.MaxConcurrentStreams > 0 {
		grpcServerOptions = append(grpcServerOptions, grpc.MaxConcurrentStreams(rOpts.MaxConcurrentStreams))
	}
	if rOpts.Keepalive != nil {
		if rOpts.Keepalive.ServerParameters != nil {
			svrParams := rOpts.Keepalive.ServerParameters
			grpcServerOptions = append(grpcServerOptions, grpc.KeepaliveParams(keepalive.ServerParameters{
				MaxConnectionIdle:     svrParams.MaxConnectionIdle,
				MaxConnectionAge:      svrParams.MaxConnectionAge,
				MaxConnectionAgeGrace: svrParams.MaxConnectionAgeGrace,
				Time:                  svrParams.Time,
				Timeout:               svrParams.Timeout,
			}))
		}
		if rOpts.Keepalive.EnforcementPolicy != nil {
			enfPol := rOpts.Keepalive.EnforcementPolicy
			grpcServerOptions = append(grpcServerOptions, grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
				MinTime:             enfPol.MinTime,
				PermitWithoutStream: enfPol.PermitWithoutStream,
			}))
		}
	}

	return grpcServerOptions
}

// ToOpenCensusReceiverServerOption checks if the TLS credentials
// in the form of a certificate file and a key file. If they aren't,
// it will return opencensusreceiver.WithNoopOption() and a nil error.
// Otherwise, it will try to retrieve gRPC transport credentials from the file combinations,
// and create a option, along with any errors encountered while retrieving the credentials.
func (tlsCreds *tlsCredentials) ToOpenCensusReceiverServerOption() (opt Option, ok bool, err error) {
	if tlsCreds == nil {
		return WithNoopOption(), false, nil
	}

	transportCreds, err := credentials.NewServerTLSFromFile(tlsCreds.CertFile, tlsCreds.KeyFile)
	if err != nil {
		return nil, false, err
	}
	gRPCCredsOpt := grpc.Creds(transportCreds)
	return WithGRPCServerOptions(gRPCCredsOpt), true, nil
}
