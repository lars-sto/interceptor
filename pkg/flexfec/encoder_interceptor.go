// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
)

// streamState holds the state for a single stream.
type streamState struct {
	mu             sync.Mutex
	flexFecEncoder FlexEncoder
	packetBuffer   []rtp.Packet

	active atomic.Value // stores activeRuntimeConfig
	stop   func()       // unsubscribe
}

// FecInterceptor implements FlexFec.
type FecInterceptor struct {
	interceptor.NoOp
	mu              sync.Mutex
	streams         map[uint32]*streamState
	numMediaPackets uint32
	numFecPackets   uint32
	encoderFactory  EncoderFactory
	cfgSrc          ConfigSource // optional runtime config source
}

// FecInterceptorFactory creates new FecInterceptors.
type FecInterceptorFactory struct {
	opts []FecOption
}

// NewFecInterceptor returns a new Fec interceptor factory.
func NewFecInterceptor(opts ...FecOption) (*FecInterceptorFactory, error) {
	return &FecInterceptorFactory{opts: opts}, nil
}

// NewInterceptor constructs a new FecInterceptor.
func (r *FecInterceptorFactory) NewInterceptor(_ string) (interceptor.Interceptor, error) {
	interceptor := &FecInterceptor{
		streams:         make(map[uint32]*streamState),
		numMediaPackets: 5,
		numFecPackets:   2,
		encoderFactory:  FlexEncoder03Factory{},
	}

	for _, opt := range r.opts {
		if err := opt(interceptor); err != nil {
			return nil, err
		}
	}

	return interceptor, nil
}

// UnbindLocalStream removes the stream state for a specific SSRC.
func (r *FecInterceptor) UnbindLocalStream(info *interceptor.StreamInfo) {
	r.mu.Lock()
	st, ok := r.streams[info.SSRC]
	if ok {
		delete(r.streams, info.SSRC)
	}
	r.mu.Unlock()

	// Unsubscribe outside lock to avoid deadlocks if source blocks
	if ok && st.stop != nil {
		st.stop()
	}
}

// BindLocalStream lets you modify any outgoing RTP packets. It is called once for per LocalStream. The returned method
// will be called once per rtp packet.
func (r *FecInterceptor) BindLocalStream(
	info *interceptor.StreamInfo, writer interceptor.RTPWriter,
) interceptor.RTPWriter {
	if info.PayloadTypeForwardErrorCorrection == 0 || info.SSRCForwardErrorCorrection == 0 {
		return writer
	}

	mediaSSRC := info.SSRC

	// Default behavior (backwards compatible): FlexFEC on with static params when negotiated
	defaultRuntime := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: r.numMediaPackets,
		NumFECPackets:   r.numFecPackets,
		CoverageMode:    CoverageModeInterleaved,
	}

	defaultActive := activeRuntimeConfig{
		enabled:          defaultRuntime.Enabled,
		numMedia:         defaultRuntime.NumMediaPackets,
		numFec:           defaultRuntime.NumFECPackets,
		coverageMode:     defaultRuntime.CoverageMode,
		interleaveStride: defaultRuntime.InterleaveStride,
		burstSpan:        defaultRuntime.BurstSpan,
	}

	stream := &streamState{
		// Chromium supports version flexfec-03 of existing draft, this is the one we configure by default.
		flexFecEncoder: r.encoderFactory.NewEncoder(info.PayloadTypeForwardErrorCorrection, info.SSRCForwardErrorCorrection),
		packetBuffer:   make([]rtp.Packet, 0, int(r.numMediaPackets)),
	}
	stream.active.Store(defaultActive)

	// Register stream under lock
	r.mu.Lock()
	r.streams[mediaSSRC] = stream
	r.mu.Unlock()

	// Optional: subscribe to runtime config updates for this stream (outside lock)
	if r.cfgSrc != nil {
		key := StreamKey{MediaSSRC: mediaSSRC}
		unsub := r.cfgSrc.Subscribe(key, func(cfg RuntimeConfig) {
			cfg = cfg.clamp(defaultRuntime)

			cur := defaultActive
			if !cfg.Enabled || cfg.NumFECPackets == 0 {
				cur.enabled = false
			} else {
				cur.enabled = true
				cur.numMedia = cfg.NumMediaPackets
				cur.numFec = cfg.NumFECPackets
				cur.coverageMode = cfg.CoverageMode
				cur.interleaveStride = cfg.InterleaveStride
				cur.burstSpan = cfg.BurstSpan
			}

			stream.active.Store(cur)
		})

		// Store unsubscribe safely (no lock required: pointer write is atomic on 64-bit,
		stream.stop = unsub
	}

	return interceptor.RTPWriterFunc(
		func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
			// Ignore non-media packets
			if header.SSRC != mediaSSRC {
				return writer.Write(header, payload, attributes)
			}

			cfgAny := stream.active.Load()
			cfg := cfgAny.(activeRuntimeConfig)
			if !cfg.enabled || cfg.numFec == 0 {
				return writer.Write(header, payload, attributes)
			}

			var fecPackets []rtp.Packet
			stream.mu.Lock()
			stream.packetBuffer = append(stream.packetBuffer, rtp.Packet{
				Header:  *header,
				Payload: payload,
			})

			// Check if we have enough packets to generate FEC
			if len(stream.packetBuffer) == int(cfg.numMedia) {
				// v1: encoder uses current interleaved coverage only
				// coverageMode/stride/span are reserved and will be enforced later in encoder/coverage
				fecPackets = stream.flexFecEncoder.EncodeFec(stream.packetBuffer, cfg.numFec)

				// Reset the packet buffer now that we've sent the corresponding FEC packets (keep capacity)
				stream.packetBuffer = stream.packetBuffer[:0]
			}
			stream.mu.Unlock()

			var errs []error
			result, err := writer.Write(header, payload, attributes)
			if err != nil {
				errs = append(errs, err)
			}

			for _, packet := range fecPackets {
				h := packet.Header
				_, err = writer.Write(&h, packet.Payload, attributes)
				if err != nil {
					errs = append(errs, err)
				}
			}

			return result, errors.Join(errs...)
		},
	)
}
