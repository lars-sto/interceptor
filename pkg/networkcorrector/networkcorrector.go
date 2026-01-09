package networkcorrector

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
)

type MediaType int

const (
	MediaUnknown MediaType = iota
	MediaAudio
	MediaVideo
)

type Factory struct {
	Interval time.Duration
}

func NewFactory() *Factory {
	return &Factory{Interval: 500 * time.Millisecond}
}

func (f *Factory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	fmt.Println("NetworkCorrector interceptor created, id=", id)
	nc := &NetworkCorrector{
		stop:     make(chan struct{}),
		interval: f.Interval,
		id:       id,
	}

	// temporary for debugging / understanding
	go nc.observe()

	return nc, nil
}

type NetworkCorrector struct {
	mu sync.RWMutex

	// per-SSRC state
	streams map[uint32]StreamState

	// SSRC -> media classification (learned from StreamInfo)
	ssrcMedia map[uint32]MediaType

	stop     chan struct{}
	interval time.Duration

	id string
}

type StreamState struct {
	SSRC         uint32
	FractionLost float64 // 0..1
	JitterRaw    uint32  // RR jitter in RTP timestamp units
	UpdatedAt    time.Time

	RTTSeconds      float64
	QueueDelayTrend float64
}

type Decision struct {
	TargetFECLevel float64
	EnableRTX      bool
	TargetNackMode string
	Reason         string
	SSRC           uint32
}

func mediaTypeFromMime(mime string) MediaType {
	m := strings.ToLower(mime)
	switch {
	case strings.HasPrefix(m, "audio/"):
		return MediaAudio
	case strings.HasPrefix(m, "video/"):
		return MediaVideo
	default:
		return MediaUnknown
	}
}

func (n *NetworkCorrector) ensureMapsLocked() {
	if n.streams == nil {
		n.streams = make(map[uint32]StreamState, 8)
	}
	if n.ssrcMedia == nil {
		n.ssrcMedia = make(map[uint32]MediaType, 8)
	}
}

func (n *NetworkCorrector) rememberStreamInfoLocked(info *interceptor.StreamInfo) {
	if info == nil {
		return
	}
	mt := mediaTypeFromMime(info.MimeType)
	if mt == MediaUnknown {
		return
	}
	n.ssrcMedia[info.SSRC] = mt
}

func (n *NetworkCorrector) BindLocalStream(info *interceptor.StreamInfo, w interceptor.RTPWriter) interceptor.RTPWriter {
	// Sender-Side (RTP out)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensureMapsLocked()
	n.rememberStreamInfoLocked(info)
	return w
}

func (n *NetworkCorrector) BindRemoteStream(info *interceptor.StreamInfo, r interceptor.RTPReader) interceptor.RTPReader {
	// Receiver-side (RTP in)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensureMapsLocked()
	n.rememberStreamInfoLocked(info)
	return r
}

func (n *NetworkCorrector) mediaOfSSRC(ssrc uint32) MediaType {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.ssrcMedia == nil {
		return MediaUnknown
	}
	return n.ssrcMedia[ssrc]
}

func (n *NetworkCorrector) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return interceptor.RTCPReaderFunc(func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
		// Sender-Side (RTCP feedback in: RR/NACK/TWCC)

		nRead, attr, err := reader.Read(b, a)
		if err != nil || nRead == 0 {
			return nRead, attr, err
		}

		pkts, uerr := rtcp.Unmarshal(b[:nRead])
		if uerr == nil {
			n.onRTCP(pkts)
		}

		return nRead, attr, err
	})
}

func (n *NetworkCorrector) onRTCP(pkts []rtcp.Packet) {
	now := time.Now()

	// 1) FEEDBACK -> STATE
	changed := n.updateNetworkStateFromRTCP(pkts, now)

	// 2) STATE -> DECISION
	if changed {
		decision, ok := n.computeDecision(now)

		if ok {
			// 3) DECISION -> APPLY (Stub: later FEC/RTX/NACK adjustment)
			n.applyDecision(decision)
		}

	}
}

func (n *NetworkCorrector) updateNetworkStateFromRTCP(pkts []rtcp.Packet, now time.Time) bool {
	gotUpdate := false

	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensureMapsLocked()

	for _, p := range pkts {
		switch pkt := p.(type) {

		case *rtcp.ReceiverReport:
			for _, rb := range pkt.Reports {
				ssrc := rb.SSRC

				st := n.streams[ssrc]
				st.SSRC = ssrc
				st.FractionLost = float64(rb.FractionLost) / 255.0
				st.JitterRaw = rb.Jitter
				st.UpdatedAt = now

				n.streams[ssrc] = st
				gotUpdate = true
			}

		case *rtcp.TransportLayerCC:
			_ = pkt // placeholder

		case *rtcp.TransportLayerNack:
			_ = pkt // placeholder
		}
	}

	return gotUpdate
}

// computeDecision picks a video SSRC (first one found) and returns a Decision.
// ok=false if no suitable video stream state exists yet.
func (n *NetworkCorrector) computeDecision(now time.Time) (d Decision, ok bool) {
	// Snapshot under read lock.
	n.mu.RLock()
	defer n.mu.RUnlock()

	if len(n.streams) == 0 {
		return Decision{}, false
	}

	// logic for decision calculation comes here

	for ssrc, st := range n.streams {
		// only act on video for now
		mt := MediaUnknown
		if n.ssrcMedia != nil {
			mt = n.ssrcMedia[ssrc]
		}
		if mt != MediaVideo {
			continue
		}
		if st.UpdatedAt.IsZero() {
			continue
		}

		return Decision{
			SSRC:           ssrc,
			TargetFECLevel: 0.0,
			EnableRTX:      true,
			TargetNackMode: "default",
			Reason:         fmt.Sprintf("ssrc=%d loss=%.3f jitterRaw=%d", ssrc, st.FractionLost, st.JitterRaw),
		}, true
	}

	return Decision{}, false
}

func (n *NetworkCorrector) applyDecision(d Decision) {
	// TODO: FEC/RTX/NACK update comes here.
	// - Update FEC
	// - activate/deactivate RTX/NACK Interceptor
	// - ...

	fmt.Printf("[NC %s] decision: %s\n", n.id, d.Reason)
}

func (n *NetworkCorrector) observe() {
	t := time.NewTicker(n.interval)
	defer t.Stop()

	for {
		select {
		case <-n.stop:
			return
		case <-t.C:
			// Snapshot under read lock and print all known streams
			n.mu.RLock()
			id := n.id

			// copy maps shallowly for iteration safety (no writes under RLock anyway)
			streams := n.streams
			media := n.ssrcMedia
			n.mu.RUnlock()

			if len(streams) == 0 {
				continue
			}

			for ssrc, st := range streams {
				if st.UpdatedAt.IsZero() {
					continue
				}
				mt := MediaUnknown
				if media != nil {
					mt = media[ssrc]
				}
				fmt.Printf("[NC %s] ssrc=%d media=%v loss=%.3f jitterRaw=%d updated=%s\n",
					id, ssrc, mt, st.FractionLost, st.JitterRaw, st.UpdatedAt.Format(time.RFC3339Nano))
			}
		}
	}
}

// No-op Interceptor plumbing
func (n *NetworkCorrector) BindRTCPWriter(w interceptor.RTCPWriter) interceptor.RTCPWriter {
	// Receiver-Side (Feedback out)
	return w
}

func (n *NetworkCorrector) UnbindLocalStream(_ *interceptor.StreamInfo)  {}
func (n *NetworkCorrector) UnbindRemoteStream(_ *interceptor.StreamInfo) {}
func (n *NetworkCorrector) Close() error                                 { close(n.stop); return nil }
