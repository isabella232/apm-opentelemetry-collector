// Copyright 2019 Omnition Inc.
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
	"log"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	jaeger "github.com/jaegertracing/jaeger/model"

	omnishardpb "github.com/signalfx/apm-opentelemetry-collector/exporter/omnishard/gen"
)

// encoderSink stores results of encoding for later examination in the tests.
type encoderSink struct {
	mutex            sync.Mutex
	successSpanCount int
	encodedRecords   []*omnishardpb.EncodedRecord
	failSpanCount    int
	failedSpans      []*jaeger.Span
}

func (es *encoderSink) onReady(
	record *omnishardpb.EncodedRecord,
	originalSpans []*jaeger.Span,
	shard *shardInMemConfig,
) {
	es.mutex.Lock()
	defer es.mutex.Unlock()

	es.successSpanCount += int(record.SpanCount)
	es.encodedRecords = append(es.encodedRecords, record)
}

func (es *encoderSink) onFail(failedSpans []*jaeger.Span, code EncoderErrorCode) {
	es.mutex.Lock()
	defer es.mutex.Unlock()

	es.failSpanCount += len(failedSpans)
	es.failedSpans = append(es.failedSpans, failedSpans...)
}

func (es *encoderSink) getSuccessSpanCount() int {
	es.mutex.Lock()
	defer es.mutex.Unlock()
	return es.successSpanCount
}

func (es *encoderSink) getEncodedRecords() []*omnishardpb.EncodedRecord {
	es.mutex.Lock()
	defer es.mutex.Unlock()
	return es.encodedRecords
}

func (es *encoderSink) getFailSpanCount() int {
	es.mutex.Lock()
	defer es.mutex.Unlock()
	return es.failSpanCount
}

func (es *encoderSink) getFailedSpans() []*jaeger.Span {
	es.mutex.Lock()
	defer es.mutex.Unlock()
	return es.failedSpans
}

// WaitForN the specific condition for up to a specified duration. Records a test error
// if time is out and condition does not become true. If error is signalled
// while waiting the function will return false, but will not record additional
// test error (we assume that signalled error is already recorded in indicateError()).
func WaitForN(t *testing.T, cond func() bool, duration time.Duration, errMsg ...interface{}) bool {
	startTime := time.Now()

	// Start with 5 ms waiting interval between condition re-evaluation.
	waitInterval := time.Millisecond * 5

	for {
		if cond() {
			return true
		}

		<-time.After(waitInterval)

		// Increase waiting interval exponentially up to 500 ms.
		if waitInterval < time.Millisecond*500 {
			waitInterval = waitInterval * 2
		}

		elapsed := time.Since(startTime)
		if elapsed > duration {
			// Waited too long, abort.
			t.Errorf("Time out after waiting %dms for %s", elapsed.Nanoseconds()/1e6, errMsg)
			return false
		}
	}
}

// WaitFor is like WaitForN but with a fixed duration of 10 seconds
func WaitFor(t *testing.T, cond func() bool, errMsg ...interface{}) bool {
	return WaitForN(t, cond, time.Second*10, errMsg...)
}

// GenRandByteString generates a string of random bytes of specified length.
func GenRandByteString(len int) string {
	b := make([]byte, len)
	for i := range b {
		b[i] = byte(rand.Intn(256))
	}
	return string(b)
}

// GetAvailableLocalAddress finds an available local port and returns an endpoint
// describing it. The port is available for opening when this function returns
// provided that there is no race by some other code to grab the same port
// immediately.
func GetAvailableLocalAddress() string {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		log.Fatalf("failed to get a free local port: %v", err)
	}
	// There is a possible race if something else takes this same port before
	// the test uses it, however, that is unlikely in practice.
	defer ln.Close()
	return ln.Addr().String()
}

// byPartitionKey implements sort.Interface for []*omnishardpb.EncodedRecord based on
// the PartitionKey field.
type byPartitionKey []*omnishardpb.EncodedRecord

func (a byPartitionKey) Len() int      { return len(a) }
func (a byPartitionKey) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a byPartitionKey) Less(i, j int) bool {
	return a[i].PartitionKey < a[j].PartitionKey
}

// clientSink is used in the tests to store responses that the client receives from
// the server.
type clientSink struct {
	mutex sync.Mutex

	responseToRecords []*omnishardpb.EncodedRecord
	responses         []*omnishardpb.ExportResponse

	failedRecords []*omnishardpb.EncodedRecord
}

func (cs *clientSink) onSendResponse(
	responseToRecords *omnishardpb.EncodedRecord,
	originalSpans []*jaeger.Span,
	response *omnishardpb.ExportResponse,
) {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	cs.responseToRecords = append(cs.responseToRecords, responseToRecords)
	cs.responses = append(cs.responses, response)
}

func (cs *clientSink) onSendFail(
	failedRecords *omnishardpb.EncodedRecord,
	originalSpans []*jaeger.Span,
	code SendErrorCode,
) {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	cs.failedRecords = append(cs.failedRecords, failedRecords)
}

func (cs *clientSink) getResponseToRecords() []*omnishardpb.EncodedRecord {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	return cs.responseToRecords
}

func (cs *clientSink) getResponses() []*omnishardpb.ExportResponse {
	cs.mutex.Lock()
	defer cs.mutex.Unlock()
	return cs.responses
}
