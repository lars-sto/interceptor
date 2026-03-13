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
	active         atomic.Value // stores activeRuntimeConfig
	stop           func()       // unsubscribe callback for runtime config updates
}

// FecInterceptor implements FlexFec.
type FecInterceptor struct {
	interceptor.NoOp
	mu              sync.Mutex
	streams         map[uint32]*streamState
	numMediaPackets uint32
	numFecPackets   uint32
	encoderFactory  EncoderFactory
	cfgSrc          ConfigSource
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

	if !ok {
		return
	}

	st.mu.Lock()
	stop := st.stop
	st.stop = nil
	st.mu.Unlock()

	if stop != nil {
		stop()
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

	initial := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: r.numMediaPackets,
		NumFECPackets:   r.numFecPackets,
	}.clamp()

	initialActive := activeRuntimeConfig{
		enabled:  initial.Enabled && initial.NumMediaPackets > 0 && initial.NumFECPackets > 0,
		numMedia: initial.NumMediaPackets,
		numFec:   initial.NumFECPackets,
	}

	stream := &streamState{
		// Chromium supports version flexfec-03 of existing draft, this is the one we configure by default
		flexFecEncoder: r.encoderFactory.NewEncoder(
			info.PayloadTypeForwardErrorCorrection, info.SSRCForwardErrorCorrection),
		packetBuffer: make([]rtp.Packet, 0, int(initial.NumMediaPackets)),
	}
	stream.active.Store(initialActive)

	// Register stream under lock
	r.mu.Lock()
	r.streams[mediaSSRC] = stream
	r.mu.Unlock()

	// Optional: subscribe to runtime config updates for this stream
	if r.cfgSrc != nil {
		key := StreamKey{MediaSSRC: mediaSSRC}
		unsub := r.cfgSrc.Subscribe(key, func(cfg RuntimeConfig) {
			stream.applyRuntimeConfig(cfg)
		})

		stream.mu.Lock()
		stream.stop = unsub
		stream.mu.Unlock()
	}

	return interceptor.RTPWriterFunc(
		func(header *rtp.Header, payload []byte, attributes interceptor.Attributes) (int, error) {
			// Ignore non-media packets
			if header.SSRC != mediaSSRC {
				return writer.Write(header, payload, attributes)
			}

			cfgAny := stream.active.Load()
			cfg := cfgAny.(activeRuntimeConfig)
			if !cfg.enabled {
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

func (s *streamState) applyRuntimeConfig(cfg RuntimeConfig) {
	cfg = cfg.clamp()

	prevAny := s.active.Load()
	prev := prevAny.(activeRuntimeConfig)

	next := prev
	next.numMedia = cfg.NumMediaPackets
	next.numFec = cfg.NumFECPackets
	next.enabled = cfg.Enabled && cfg.NumMediaPackets > 0 && cfg.NumFECPackets > 0

	// Reset buffered media when disabling or changing batch size to avoid
	// mixing packets collected under different runtime settings
	needReset := (!next.enabled && prev.enabled) ||
		(next.enabled && prev.enabled && next.numMedia != prev.numMedia)

	if needReset {
		s.mu.Lock()
		s.packetBuffer = s.packetBuffer[:0]
		s.mu.Unlock()
	}

	// Publish the new active config for subsequent writes. A write that has already
	// loaded the previous config may still complete under the old settings
	s.active.Store(next)
}
