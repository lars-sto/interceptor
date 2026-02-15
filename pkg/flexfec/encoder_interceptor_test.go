// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec_test

import (
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/internal/test"
	"github.com/pion/interceptor/pkg/flexfec"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
)

type MockFlexEncoder struct {
	mu            sync.Mutex
	Called        bool
	MediaPackets  []rtp.Packet
	NumFECPackets uint32
	FECPackets    []rtp.Packet
}

func NewMockFlexEncoder(fecPackets []rtp.Packet) *MockFlexEncoder {
	return &MockFlexEncoder{
		Called:     false,
		FECPackets: fecPackets,
	}
}

func (m *MockFlexEncoder) EncodeFec(mediaPackets []rtp.Packet, numFecPackets uint32) []rtp.Packet {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Called = true
	m.MediaPackets = mediaPackets
	m.NumFECPackets = numFecPackets
	return m.FECPackets
}

type MockEncoderFactory struct {
	Called      bool
	PayloadType uint8
	SSRC        uint32
	Encoder     flexfec.FlexEncoder
}

func NewMockEncoderFactory(encoder flexfec.FlexEncoder) *MockEncoderFactory {
	return &MockEncoderFactory{
		Called:  false,
		Encoder: encoder,
	}
}

func (m *MockEncoderFactory) NewEncoder(payloadType uint8, ssrc uint32) flexfec.FlexEncoder {
	m.Called = true
	m.PayloadType = payloadType
	m.SSRC = ssrc

	return m.Encoder
}

type testConfigSource struct {
	mu sync.Mutex

	cbBySSRC map[uint32]func(cfg flexfec.RuntimeConfig)
	unsubCnt map[uint32]int
}

func newTestConfigSource() *testConfigSource {
	return &testConfigSource{
		cbBySSRC: make(map[uint32]func(cfg flexfec.RuntimeConfig)),
		unsubCnt: make(map[uint32]int),
	}
}

func (s *testConfigSource) Subscribe(key flexfec.StreamKey, fn func(cfg flexfec.RuntimeConfig)) (unsubscribe func()) {
	s.mu.Lock()
	s.cbBySSRC[key.MediaSSRC] = fn
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		s.unsubCnt[key.MediaSSRC]++
		delete(s.cbBySSRC, key.MediaSSRC)
		s.mu.Unlock()
	}
}

func (s *testConfigSource) publish(ssrc uint32, cfg flexfec.RuntimeConfig) {
	s.mu.Lock()
	fn := s.cbBySSRC[ssrc]
	s.mu.Unlock()
	if fn != nil {
		fn(cfg)
	}
}

func (s *testConfigSource) unsubscribes(ssrc uint32) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unsubCnt[ssrc]
}

func TestFecInterceptor_GenerateAndWriteFecPackets(t *testing.T) {
	fecPackets := []rtp.Packet{
		{
			Header: rtp.Header{
				SSRC:           2000,
				PayloadType:    100,
				SequenceNumber: 1000,
			},
			Payload: []byte{0xFE, 0xC0, 0xDE},
		},
	}
	mockEncoder := NewMockFlexEncoder(fecPackets)
	mockFactory := NewMockEncoderFactory(mockEncoder)

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(mockFactory),
		flexfec.NumMediaPackets(2),
		flexfec.NumFECPackets(1),
	)
	assert.NoError(t, err)

	i, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              1000,
		PayloadTypeForwardErrorCorrection: 100,
		SSRCForwardErrorCorrection:        2000,
	}

	stream := test.NewMockStream(info, i)
	defer assert.NoError(t, stream.Close())

	assert.True(t, mockFactory.Called, "NewEncoder should have been called")
	assert.Equal(t, uint8(100), mockFactory.PayloadType, "Should be called with correct payload type")
	assert.Equal(t, uint32(2000), mockFactory.SSRC, "Should be called with correct SSRC")

	for i := uint16(1); i <= 2; i++ {
		packet := &rtp.Packet{
			Header: rtp.Header{
				SSRC:           1000,
				SequenceNumber: i,
				PayloadType:    96,
			},
			Payload: []byte{0x01, 0x02, 0x03, 0x04},
		}
		err = stream.WriteRTP(packet)
		assert.NoError(t, err)
	}

	var mediaPacketsCount, fecPacketsCount int
	for i := 0; i < 3; i++ {
		select {
		case packet := <-stream.WrittenRTP():
			switch packet.PayloadType {
			case 96:
				mediaPacketsCount++
			case 100:
				fecPacketsCount++
				assert.Equal(t, uint32(2000), packet.SSRC)
				assert.Equal(t, []byte{0xFE, 0xC0, 0xDE}, packet.Payload)
			}
		default:
			assert.Fail(t, "Not enough packets were written")
		}
	}

	assert.Equal(t, 2, mediaPacketsCount, "Should have written 2 media packets")
	assert.Equal(t, 1, fecPacketsCount, "Should have written 1 FEC packet")
	assert.True(t, mockEncoder.Called, "EncodeFec should have been called")
	assert.Equal(t, uint32(1), mockEncoder.NumFECPackets, "Should be called with correct number of FEC packets")
}

func TestFecInterceptor_BypassStreamWhenFecPtAndSsrcAreZero(t *testing.T) {
	fecPackets := []rtp.Packet{
		{
			Header: rtp.Header{
				SSRC:           2000,
				PayloadType:    100,
				SequenceNumber: 1000,
			},
			Payload: []byte{0xFE, 0xC0, 0xDE},
		},
	}
	mockEncoder := NewMockFlexEncoder(fecPackets)
	mockFactory := NewMockEncoderFactory(mockEncoder)

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(mockFactory),
		flexfec.NumMediaPackets(1),
		flexfec.NumFECPackets(1),
	)
	assert.NoError(t, err)

	i, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              1,
		PayloadTypeForwardErrorCorrection: 0,
		SSRCForwardErrorCorrection:        0,
	}

	stream := test.NewMockStream(info, i)
	defer assert.NoError(t, stream.Close())

	packet := &rtp.Packet{
		Header: rtp.Header{
			SSRC:        1,
			PayloadType: 96,
		},
		Payload: []byte{0x01, 0x02, 0x03, 0x04},
	}

	err = stream.WriteRTP(packet)
	assert.NoError(t, err)

	select {
	case writtenPacket := <-stream.WrittenRTP():
		assert.Equal(t, packet.SSRC, writtenPacket.SSRC)
		assert.Equal(t, packet.SequenceNumber, writtenPacket.SequenceNumber)
		assert.Equal(t, packet.Payload, writtenPacket.Payload)
	default:
		assert.Fail(t, "No packet was written")
	}

	select {
	case <-stream.WrittenRTP():
		assert.Fail(t, "Only one packet must be received")
	default:
	}

	assert.False(t, mockEncoder.Called, "EncodeFec should not have been called")
}

func TestFecInterceptor_EncodeOnlyPacketsWithMediaSsrc(t *testing.T) {
	mockEncoder := NewMockFlexEncoder(nil)
	mockFactory := NewMockEncoderFactory(mockEncoder)

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(mockFactory),
		flexfec.NumMediaPackets(2),
		flexfec.NumFECPackets(1),
	)
	assert.NoError(t, err)

	i, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              1000,
		PayloadTypeForwardErrorCorrection: 100,
		SSRCForwardErrorCorrection:        2000,
	}

	stream := test.NewMockStream(info, i)
	defer assert.NoError(t, stream.Close())

	mediaPacket := &rtp.Packet{
		Header: rtp.Header{
			SSRC:           1000,
			SequenceNumber: 1,
			PayloadType:    96,
		},
		Payload: []byte{0x01, 0x02, 0x03, 0x04},
	}

	nonMediaPacket := &rtp.Packet{
		Header: rtp.Header{
			SSRC:           3000, // Different from mediaSSRC
			SequenceNumber: 2,
			PayloadType:    96,
		},
		Payload: []byte{0x05, 0x06, 0x07, 0x08},
	}

	err = stream.WriteRTP(mediaPacket)
	assert.NoError(t, err)

	err = stream.WriteRTP(nonMediaPacket)
	assert.NoError(t, err)

	// The non-media packet should be passed through without being added to the buffer
	select {
	case writtenPacket := <-stream.WrittenRTP():
		assert.Equal(t, mediaPacket.SSRC, writtenPacket.SSRC)
	default:
		assert.Fail(t, "No media packet was written")
	}

	select {
	case writtenPacket := <-stream.WrittenRTP():
		assert.Equal(t, nonMediaPacket.SSRC, writtenPacket.SSRC)
	default:
		assert.Fail(t, "No non-media packet was written")
	}

	assert.False(t, mockEncoder.Called, "EncodeFec should not have been called")
}

type EncoderFactoryFunc func(payloadType uint8, ssrc uint32) flexfec.FlexEncoder

func (f EncoderFactoryFunc) NewEncoder(payloadType uint8, ssrc uint32) flexfec.FlexEncoder {
	return f(payloadType, ssrc)
}

// nolint:cyclop
func TestFecInterceptor_HandleMultipleStreamsCorrectly(t *testing.T) {
	fecPackets1 := []rtp.Packet{
		{
			Header: rtp.Header{
				SSRC:           2000,
				PayloadType:    100,
				SequenceNumber: 1000,
			},
			Payload: []byte{0xFE, 0xC0, 0xDE},
		},
	}
	mockEncoder1 := NewMockFlexEncoder(fecPackets1)

	fecPackets2 := []rtp.Packet{
		{
			Header: rtp.Header{
				SSRC:           3000,
				PayloadType:    101,
				SequenceNumber: 1000,
			},
			Payload: []byte{0xFE, 0xC0, 0xDE},
		},
	}
	mockEncoder2 := NewMockFlexEncoder(fecPackets2)

	customFactory := EncoderFactoryFunc(func(payloadType uint8, ssrc uint32) flexfec.FlexEncoder {
		if payloadType == 100 && ssrc == 2000 {
			return mockEncoder1
		} else if payloadType == 101 && ssrc == 3000 {
			return mockEncoder2
		}

		return nil
	})

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(customFactory),
		flexfec.NumMediaPackets(2),
	)
	assert.NoError(t, err)

	fecInterceptor, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info1 := &interceptor.StreamInfo{
		SSRC:                              1000,
		PayloadTypeForwardErrorCorrection: 100,
		SSRCForwardErrorCorrection:        2000,
	}

	info2 := &interceptor.StreamInfo{
		SSRC:                              1001,
		PayloadTypeForwardErrorCorrection: 101,
		SSRCForwardErrorCorrection:        3000,
	}

	stream1 := test.NewMockStream(info1, fecInterceptor)
	defer assert.NoError(t, stream1.Close())

	stream2 := test.NewMockStream(info2, fecInterceptor)
	defer assert.NoError(t, stream2.Close())

	for idx := uint16(1); idx <= 2; idx++ {
		packet1 := &rtp.Packet{
			Header: rtp.Header{
				SSRC:           1000,
				SequenceNumber: idx,
				PayloadType:    96,
			},
			Payload: []byte{0x01, 0x02, 0x03, 0x04},
		}
		err = stream1.WriteRTP(packet1)
		assert.NoError(t, err)

		packet2 := &rtp.Packet{
			Header: rtp.Header{
				SSRC:           1001,
				SequenceNumber: idx,
				PayloadType:    97,
			},
			Payload: []byte{0x05, 0x06, 0x07, 0x08},
		}
		err = stream2.WriteRTP(packet2)
		assert.NoError(t, err)
	}

	assert.True(t, mockEncoder1.Called, "First encoder's EncodeFec should have been called")
	assert.True(t, mockEncoder2.Called, "Second encoder's EncodeFec should have been called")

	mediaPacketsCount1 := 0
	fecPacketsCount1 := 0
	for i := 0; i < 3; i++ {
		select {
		case packet := <-stream1.WrittenRTP():
			switch packet.SSRC {
			case 1000:
				mediaPacketsCount1++
			case 2000:
				fecPacketsCount1++
			}
		default:
			assert.Fail(t, "No packet from stream1")
		}
	}
	assert.Equal(t, 2, mediaPacketsCount1, "Expected 2 media packets for stream1")
	assert.Equal(t, 1, fecPacketsCount1, "Expected 1 FEC packet for stream1")

	mediaPacketsCount2 := 0
	fecPacketsCount2 := 0
	for i := 0; i < 3; i++ {
		select {
		case packet := <-stream2.WrittenRTP():
			switch packet.SSRC {
			case 1001:
				mediaPacketsCount2++
			case 3000:
				fecPacketsCount2++
			}
		default:
			assert.Fail(t, "No packet from stream2")
		}
	}
	assert.Equal(t, 2, mediaPacketsCount2, "Expected 2 media packets for stream2")
	assert.Equal(t, 1, fecPacketsCount2, "Expected 1 FEC packet for stream2")
}

/*
TestFecInterceptor_RuntimeConfigUpdateChangesBatchSize verifies that:
- the interceptor subscribes to a ConfigSource (if provided)
- runtime updates change the active batch size (NumMediaPackets) and NumFECPackets
- after an update, FEC packets are generated according to the new config
*/
func TestFecInterceptor_RuntimeConfigUpdateChangesBatchSize(t *testing.T) {
	const (
		mediaSSRC = uint32(1000)
		fecSSRC   = uint32(2000)
		fecPT     = uint8(100)
	)

	fecPackets := []rtp.Packet{
		{
			Header: rtp.Header{
				SSRC:           fecSSRC,
				PayloadType:    fecPT,
				SequenceNumber: 1000,
			},
			Payload: []byte{0xFE, 0xC0, 0xDE},
		},
	}

	mockEncoder := NewMockFlexEncoder(fecPackets)
	mockFactory := NewMockEncoderFactory(mockEncoder)
	src := newTestConfigSource()

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(mockFactory),
		// Static defaults (intentionally different from runtime update)
		flexfec.NumMediaPackets(5),
		flexfec.NumFECPackets(2),
		flexfec.WithConfigSource(src),
	)
	assert.NoError(t, err)

	i, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              mediaSSRC,
		PayloadTypeForwardErrorCorrection: fecPT,
		SSRCForwardErrorCorrection:        fecSSRC,
	}

	stream := test.NewMockStream(info, i)
	defer assert.NoError(t, stream.Close())

	// Apply runtime config: smaller batch -> should emit after 2 media packets
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 2,
		NumFECPackets:   1,
		CoverageMode:    flexfec.CoverageModeInterleaved,
	})

	// Write two media packets. We expect 2 media + 1 FEC written
	for seq := uint16(1); seq <= 2; seq++ {
		p := &rtp.Packet{
			Header: rtp.Header{
				SSRC:           mediaSSRC,
				SequenceNumber: seq,
				PayloadType:    96,
				Timestamp:      uint32(time.Now().UnixNano()),
			},
			Payload: []byte{0x01, 0x02, 0x03, 0x04},
		}
		err = stream.WriteRTP(p)
		assert.NoError(t, err)
	}

	var mediaCount, fecCount int
	for i := 0; i < 3; i++ {
		select {
		case pkt := <-stream.WrittenRTP():
			switch pkt.PayloadType {
			case 96:
				mediaCount++
			case fecPT:
				fecCount++
				assert.Equal(t, fecSSRC, pkt.SSRC)
				assert.Equal(t, []byte{0xFE, 0xC0, 0xDE}, pkt.Payload)
			}
		default:
			assert.Fail(t, "Not enough packets were written")
		}
	}

	assert.Equal(t, 2, mediaCount)
	assert.Equal(t, 1, fecCount)

	assert.True(t, mockFactory.Called, "NewEncoder should have been called")
	assert.True(t, mockEncoder.Called, "EncodeFec should have been called")
	assert.Equal(t, uint32(1), mockEncoder.NumFECPackets, "EncodeFec should use runtime NumFECPackets")
}

/*
TestFecInterceptor_RuntimeConfigDisableBypasses verifies that:
  - when runtime config sets Enabled=false (or NumFECPackets=0),
    the interceptor bypasses encoding and only forwards media packets
*/
func TestFecInterceptor_RuntimeConfigDisableBypasses(t *testing.T) {
	const (
		mediaSSRC = uint32(1000)
		fecSSRC   = uint32(2000)
		fecPT     = uint8(100)
	)

	mockEncoder := NewMockFlexEncoder(nil)
	mockFactory := NewMockEncoderFactory(mockEncoder)
	src := newTestConfigSource()

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(mockFactory),
		flexfec.NumMediaPackets(2),
		flexfec.NumFECPackets(1),
		flexfec.WithConfigSource(src),
	)
	assert.NoError(t, err)

	i, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              mediaSSRC,
		PayloadTypeForwardErrorCorrection: fecPT,
		SSRCForwardErrorCorrection:        fecSSRC,
	}

	stream := test.NewMockStream(info, i)
	defer assert.NoError(t, stream.Close())

	// Disable via runtime config
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         false,
		NumMediaPackets: 2,
		NumFECPackets:   1,
	})

	// Write enough packets that would normally trigger encoding
	for seq := uint16(1); seq <= 2; seq++ {
		p := &rtp.Packet{
			Header: rtp.Header{
				SSRC:           mediaSSRC,
				SequenceNumber: seq,
				PayloadType:    96,
			},
			Payload: []byte{0xAA, 0xBB},
		}
		err = stream.WriteRTP(p)
		assert.NoError(t, err)
	}

	// Expect only 2 media packets written, no FEC
	var mediaCount, fecCount int
	for i := 0; i < 2; i++ {
		select {
		case pkt := <-stream.WrittenRTP():
			if pkt.PayloadType == 96 {
				mediaCount++
			}
			if pkt.PayloadType == fecPT {
				fecCount++
			}
		default:
			assert.Fail(t, "Not enough packets were written")
		}
	}

	// Ensure no extra packets were written.
	select {
	case <-stream.WrittenRTP():
		assert.Fail(t, "Unexpected extra packet written")
	default:
	}

	assert.Equal(t, 2, mediaCount)
	assert.Equal(t, 0, fecCount)
	assert.False(t, mockEncoder.Called, "EncodeFec should not be called when disabled")
}

/*
TestFecInterceptor_UnbindUnsubscribes verifies that:
  - the unsubscribe function returned by ConfigSource.Subscribe is invoked
    when UnbindLocalStream is called for the stream SSRC
*/
func TestFecInterceptor_UnbindUnsubscribes(t *testing.T) {
	const (
		mediaSSRC = uint32(1000)
		fecSSRC   = uint32(2000)
		fecPT     = uint8(100)
	)

	mockEncoder := NewMockFlexEncoder(nil)
	mockFactory := NewMockEncoderFactory(mockEncoder)
	src := newTestConfigSource()

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(mockFactory),
		flexfec.WithConfigSource(src),
	)
	assert.NoError(t, err)

	itc, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              mediaSSRC,
		PayloadTypeForwardErrorCorrection: fecPT,
		SSRCForwardErrorCorrection:        fecSSRC,
	}

	stream := test.NewMockStream(info, itc)

	// Wichtig: mindestens 1x schreiben, damit Bind/Subscribe wirklich passiert
	err = stream.WriteRTP(&rtp.Packet{
		Header: rtp.Header{
			SSRC:           mediaSSRC,
			SequenceNumber: 1,
			PayloadType:    96,
		},
		Payload: []byte{0x01},
	})
	assert.NoError(t, err)
	itc.UnbindLocalStream(info)

	assert.Equal(t, 1, src.unsubscribes(mediaSSRC), "Expected exactly one unsubscribe call")
}

// Encoder that returns a distinct FEC packet per call so we can assert we got 2 batches
type togglingEncoder struct {
	mu        sync.Mutex
	callCount int
}

func (e *togglingEncoder) EncodeFec(mediaPackets []rtp.Packet, numFecPackets uint32) []rtp.Packet {
	const (
		mediaSSRC = uint32(1000)
		fecSSRC   = uint32(2000)
		fecPT     = uint8(100)
	)

	e.mu.Lock()
	defer e.mu.Unlock()
	e.callCount++

	out := make([]rtp.Packet, 0, numFecPackets)
	for i := uint32(0); i < numFecPackets; i++ {
		out = append(out, rtp.Packet{
			Header: rtp.Header{
				SSRC:           fecSSRC,
				PayloadType:    fecPT,
				SequenceNumber: uint16(1000 + e.callCount), // distinct per batch
			},
			// last byte encodes which batch this was (1 then 2)
			Payload: []byte{0xFE, 0xC0, 0xDE, byte(e.callCount)},
		})
	}
	return out
}

type CountingFlexEncoder struct {
	mu        sync.Mutex
	CallCount int

	FecSSRC uint32
	FecPT   uint8
}

func NewCountingFlexEncoder(fecSSRC uint32, fecPT uint8) *CountingFlexEncoder {
	return &CountingFlexEncoder{
		FecSSRC: fecSSRC,
		FecPT:   fecPT,
	}
}

func (e *CountingFlexEncoder) EncodeFec(mediaPackets []rtp.Packet, numFecPackets uint32) []rtp.Packet {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.CallCount++

	// return exactly numFecPackets packets; marker last payload byte = call count
	out := make([]rtp.Packet, 0, numFecPackets)
	for i := uint32(0); i < numFecPackets; i++ {
		out = append(out, rtp.Packet{
			Header: rtp.Header{
				SSRC:           e.FecSSRC,
				PayloadType:    e.FecPT,
				SequenceNumber: uint16(1000 + e.CallCount),
			},
			Payload: []byte{0xFE, 0xC0, 0xDE, byte(e.CallCount)},
		})
	}
	return out
}

func readN(t *testing.T, ch <-chan *rtp.Packet, n int, timeout time.Duration) []*rtp.Packet {
	t.Helper()

	out := make([]*rtp.Packet, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for len(out) < n {
		select {
		case pkt := <-ch:
			out = append(out, pkt)
		case <-deadline.C:
			assert.Fail(t, "timeout waiting for packets")
			return out
		}
	}
	return out
}

func TestFecInterceptor_RuntimeConfigToggleOnOffOnSendsTwoFecBatches(t *testing.T) {
	const (
		mediaSSRC = uint32(1000)
		fecSSRC   = uint32(2000)
		fecPT     = uint8(100)
	)

	enc := NewCountingFlexEncoder(fecSSRC, fecPT)
	src := newTestConfigSource()

	customFactory := EncoderFactoryFunc(func(payloadType uint8, ssrc uint32) flexfec.FlexEncoder {
		assert.Equal(t, fecPT, payloadType)
		assert.Equal(t, fecSSRC, ssrc)
		return enc
	})

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(customFactory),
		flexfec.NumMediaPackets(5),
		flexfec.NumFECPackets(2),
		flexfec.WithConfigSource(src),
	)
	assert.NoError(t, err)

	itc, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              mediaSSRC,
		PayloadTypeForwardErrorCorrection: fecPT,
		SSRCForwardErrorCorrection:        fecSSRC,
	}
	stream := test.NewMockStream(info, itc)
	defer assert.NoError(t, stream.Close())

	writeMedia := func(startSeq uint16, n int) {
		for i := 0; i < n; i++ {
			p := &rtp.Packet{
				Header: rtp.Header{
					SSRC:           mediaSSRC,
					SequenceNumber: startSeq + uint16(i),
					PayloadType:    96,
				},
				Payload: []byte{0x01, 0x02, 0x03},
			}
			assert.NoError(t, stream.WriteRTP(p))
		}
	}

	// Phase 1: ON, batch=2 => after 2 media -> 1 FEC
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 2,
		NumFECPackets:   1,
		CoverageMode:    flexfec.CoverageModeInterleaved,
	})
	writeMedia(1, 2)
	p1 := readN(t, stream.WrittenRTP(), 3, 500*time.Millisecond) // 2 media + 1 fec

	// Phase 2: OFF => after 2 media -> 0 FEC
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         false,
		NumMediaPackets: 2,
		NumFECPackets:   1,
	})
	writeMedia(3, 2)
	p2 := readN(t, stream.WrittenRTP(), 2, 500*time.Millisecond) // only 2 media

	// Phase 3: ON again, batch=2 => after 2 media -> 1 FEC
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 2,
		NumFECPackets:   1,
		CoverageMode:    flexfec.CoverageModeInterleaved,
	})
	writeMedia(5, 2)
	p3 := readN(t, stream.WrittenRTP(), 3, 500*time.Millisecond) // 2 media + 1 fec

	// Assertions: phase-wise
	countFec := func(pkts []*rtp.Packet) (media, fec int, markers []byte) {
		for _, pkt := range pkts {
			if pkt.PayloadType == 96 {
				media++
			}
			if pkt.PayloadType == fecPT {
				fec++
				assert.Equal(t, fecSSRC, pkt.SSRC)
				if len(pkt.Payload) > 0 {
					markers = append(markers, pkt.Payload[len(pkt.Payload)-1])
				}
			}
		}
		return
	}

	m1, f1, mk1 := countFec(p1)
	m2, f2, _ := countFec(p2)
	m3, f3, mk3 := countFec(p3)

	assert.Equal(t, 2, m1)
	assert.Equal(t, 1, f1)
	assert.Equal(t, []byte{1}, mk1)

	assert.Equal(t, 2, m2)
	assert.Equal(t, 0, f2)

	assert.Equal(t, 2, m3)
	assert.Equal(t, 1, f3)
	assert.Equal(t, []byte{2}, mk3)

	enc.mu.Lock()
	defer enc.mu.Unlock()
	assert.Equal(t, 2, enc.CallCount, "encoder should be called exactly twice")
}

func TestFecInterceptor_DisableMidBufferResetsStateBeforeReenable(t *testing.T) {
	const (
		mediaSSRC = uint32(1000)
		fecSSRC   = uint32(2000)
		fecPT     = uint8(100)
	)

	enc := NewCountingFlexEncoder(fecSSRC, fecPT)
	src := newTestConfigSource()

	customFactory := EncoderFactoryFunc(func(payloadType uint8, ssrc uint32) flexfec.FlexEncoder {
		return enc
	})

	factory, err := flexfec.NewFecInterceptor(
		flexfec.FECEncoderFactory(customFactory),
		flexfec.WithConfigSource(src),
	)
	assert.NoError(t, err)

	itc, err := factory.NewInterceptor("")
	assert.NoError(t, err)

	info := &interceptor.StreamInfo{
		SSRC:                              mediaSSRC,
		PayloadTypeForwardErrorCorrection: fecPT,
		SSRCForwardErrorCorrection:        fecSSRC,
	}
	stream := test.NewMockStream(info, itc)
	defer assert.NoError(t, stream.Close())

	writeOne := func(seq uint16) {
		p := &rtp.Packet{
			Header: rtp.Header{
				SSRC:           mediaSSRC,
				SequenceNumber: seq,
				PayloadType:    96,
			},
			Payload: []byte{0x11},
		}
		assert.NoError(t, stream.WriteRTP(p))
	}

	// Enable with batch=4
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 4,
		NumFECPackets:   1,
		CoverageMode:    flexfec.CoverageModeInterleaved,
	})

	// Write 2 (half full). Expect only 2 media, no FEC
	writeOne(1)
	writeOne(2)
	pA := readN(t, stream.WrittenRTP(), 2, 500*time.Millisecond)
	for _, pkt := range pA {
		assert.Equal(t, uint8(96), pkt.PayloadType)
	}

	// Disable (should reset buffer state)
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         false,
		NumMediaPackets: 4,
		NumFECPackets:   1,
	})

	// While disabled, writes are pass-through, still no FEC.
	writeOne(3)
	writeOne(4)
	pB := readN(t, stream.WrittenRTP(), 2, 500*time.Millisecond)
	for _, pkt := range pB {
		assert.Equal(t, uint8(96), pkt.PayloadType)
	}

	// Re-enable with batch=4 again.
	src.publish(mediaSSRC, flexfec.RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 4,
		NumFECPackets:   1,
		CoverageMode:    flexfec.CoverageModeInterleaved,
	})

	// Now: first 3 new packets should still not produce FEC.
	writeOne(5)
	writeOne(6)
	writeOne(7)
	pC := readN(t, stream.WrittenRTP(), 3, 500*time.Millisecond)
	for _, pkt := range pC {
		assert.Equal(t, uint8(96), pkt.PayloadType)
	}

	// 4th new packet should trigger 1 FEC: total 2 packets written (media + fec)
	writeOne(8)
	pD := readN(t, stream.WrittenRTP(), 2, 500*time.Millisecond)

	mediaCnt, fecCnt := 0, 0
	for _, pkt := range pD {
		if pkt.PayloadType == 96 {
			mediaCnt++
		}
		if pkt.PayloadType == fecPT {
			fecCnt++
		}
	}
	assert.Equal(t, 1, mediaCnt)
	assert.Equal(t, 1, fecCnt)

	enc.mu.Lock()
	defer enc.mu.Unlock()
	assert.Equal(t, 1, enc.CallCount, "expected exactly one encode call after re-enable")
}
