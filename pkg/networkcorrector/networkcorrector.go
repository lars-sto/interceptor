package networkcorrector

import (
	"fmt"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
)

type Factory struct {
	Interval time.Duration
}

func NewFactory() *Factory {
	return &Factory{Interval: 500 * time.Millisecond}
}

// NewInterceptor erfüllt interceptor.Factory
func (f *Factory) NewInterceptor(id string) (interceptor.Interceptor, error) {
	fmt.Println("NetworkCorrector interceptor created, id=", id)
	nc := &NetworkCorrector{
		stop:     make(chan struct{}),
		interval: f.Interval,
	}

	// Optional: nur fürs Debugging/Verstehen
	go nc.observe()

	return nc, nil
}

type NetworkCorrector struct {
	mu       sync.RWMutex
	state    NetworkState
	stop     chan struct{}
	interval time.Duration
}

type NetworkState struct {
	FractionLost float64 // 0..1
	JitterRaw    uint32  // RR jitter in RTP timestamp units
	UpdatedAt    time.Time

	RTTSeconds      float64
	QueueDelayTrend float64
}

type Decision struct {
	// Platzhalter: später echte Stellgrößen
	TargetFECLevel float64
	EnableRTX      bool
	TargetNackMode string
	Reason         string
}

func (n *NetworkCorrector) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return interceptor.RTCPReaderFunc(func(b []byte, a interceptor.Attributes) (int, interceptor.Attributes, error) {
		nRead, attr, err := reader.Read(b, a)
		if err != nil || nRead == 0 {
			return nRead, attr, err
		}

		pkts, uerr := rtcp.Unmarshal(b[:nRead])
		if uerr == nil {
			n.onRTCP(pkts) // hier aktualisierst du deinen NetworkState
		}

		return nRead, attr, err
	})
}

func (n *NetworkCorrector) onRTCP(pkts []rtcp.Packet) {
	now := time.Now()

	// 1) FEEDBACK -> STATE (nur lesen/parsen/ableiten)
	changed := n.updateNetworkStateFromRTCP(pkts, now)

	// 2) STATE -> DECISION (Regler)
	// Nur wenn es neue relevante Infos gab (optional)
	if changed {
		decision := n.computeDecision(now)

		// 3) DECISION -> APPLY (Stub: später FEC/RTX/NACK steuern)
		n.applyDecision(decision)
	}
}

func (n *NetworkCorrector) updateNetworkStateFromRTCP(pkts []rtcp.Packet, now time.Time) bool {
	var (
		gotUpdate bool
		newState  NetworkState
	)

	// Ausgangspunkt: alter State übernehmen (damit nicht alles resetet)
	n.mu.RLock()
	newState = n.state
	n.mu.RUnlock()

	for _, p := range pkts {
		switch pkt := p.(type) {

		case *rtcp.ReceiverReport:
			// minimal: wir nehmen den ersten Reportblock
			for _, rb := range pkt.Reports {
				newState.FractionLost = float64(rb.FractionLost) / 255.0
				newState.JitterRaw = rb.Jitter
				newState.UpdatedAt = now

				gotUpdate = true
				break
			}

		case *rtcp.TransportLayerCC:
			// Platzhalter: später TWCC dekodieren -> queueing / delay trend
			// newState.QueueDelayTrend = ...
			newState.UpdatedAt = now
			gotUpdate = true

		case *rtcp.TransportLayerNack:
			// Platzhalter: später burst loss / repair pressure
			// z.B. newState.NackRate = ...
		}
	}

	if !gotUpdate {
		return false
	}

	n.mu.Lock()
	n.state = newState
	n.mu.Unlock()

	return true
}

func (n *NetworkCorrector) computeDecision(now time.Time) Decision {
	n.mu.RLock()
	s := n.state
	n.mu.RUnlock()

	// logic for decision calculation comes here

	return Decision{
		TargetFECLevel: 0.0,
		EnableRTX:      true,
		TargetNackMode: "default",
		Reason:         fmt.Sprintf("loss=%.3f jitterRaw=%d", s.FractionLost, s.JitterRaw),
	}
}

func (n *NetworkCorrector) applyDecision(d Decision) {
	// TODO: FEC/RTX/NACK update comes here.
	// - Update FEC
	// - activate/deactivate RTX/NACK Interceptor
	// - ...

	fmt.Println("[NC decision]", d.Reason)
}

func (n *NetworkCorrector) observe() {
	t := time.NewTicker(n.interval)
	defer t.Stop()

	for {
		select {
		case <-n.stop:
			return
		case <-t.C:
			n.mu.RLock()
			s := n.state
			n.mu.RUnlock()

			if s.UpdatedAt.IsZero() {
				// gets triggered on receiver side
				// fmt.Println("[nc] no RR yet")
				continue
			}
			fmt.Printf("[NC] loss=%.3f jitterRaw=%d updated=%s\n",
				s.FractionLost, s.JitterRaw, s.UpdatedAt.Format(time.RFC3339Nano))
		}
	}
}

// No-op Interceptor plumbing
func (n *NetworkCorrector) BindRTCPWriter(w interceptor.RTCPWriter) interceptor.RTCPWriter { return w }
func (n *NetworkCorrector) BindLocalStream(_ *interceptor.StreamInfo, w interceptor.RTPWriter) interceptor.RTPWriter {
	return w
}
func (n *NetworkCorrector) UnbindLocalStream(_ *interceptor.StreamInfo) {}
func (n *NetworkCorrector) BindRemoteStream(_ *interceptor.StreamInfo, r interceptor.RTPReader) interceptor.RTPReader {
	return r
}
func (n *NetworkCorrector) UnbindRemoteStream(_ *interceptor.StreamInfo) {}
func (n *NetworkCorrector) Close() error                                 { close(n.stop); return nil }
