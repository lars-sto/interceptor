package networkcorrector

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/transport/v3/vnet"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/stretchr/testify/assert"
)

const (
	numPackets = 200
)

func TestNetworkcorrector(t *testing.T) {
	// Create interceptorRegistry with default interceptots
	interceptorRegistry := &interceptor.Registry{}

	// Create mediaEngine with default codecs
	mediaEngine := &webrtc.MediaEngine{}
	err := mediaEngine.RegisterDefaultCodecs()
	assert.NoError(t, err)

	// Configure flexfec-03
	err = webrtc.ConfigureFlexFEC03(49, mediaEngine, interceptorRegistry)
	assert.NoError(t, err)

	err = webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry)
	assert.NoError(t, err)

	pcOffer, pcAnswer, wan := createVNetPair(t, interceptorRegistry)

	wan.AddChunkFilter(func(c vnet.Chunk) bool {
		h := &rtp.Header{}
		if _, err := h.Unmarshal(c.UserData()); err != nil {
			return true
		}

		// packet drop logic here

		return true
	})

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "video", "pion")
	assert.NoError(t, err)

	rtpSender, err := pcOffer.AddTrack(track)
	assert.NoError(t, err)

	done := make(chan struct{})

	// Sender RTCP feedback receive stream
	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			n, _, rtcpErr := rtpSender.Read(rtcpBuf)
			if rtcpErr != nil {
				return
			}

			ps, err2 := rtcp.Unmarshal(rtcpBuf[:n])
			assert.NoError(t, err2)

			for _, p := range ps {
				pn, ok := p.(*rtcp.TransportLayerNack)
				if !ok {
					continue
				}
				fmt.Printf("Received RTCP Packet %s\n", pn)

			}
		}
	}()

	// RTP Receive Stream
	pcAnswer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		received := 0

		for {
			pkt, _, readRTPErr := track.ReadRTP()
			if errors.Is(readRTPErr, io.EOF) {
				return
			}
			if readRTPErr != nil {
				return
			}
			fmt.Printf("[Receiver] received packet (seq=%d)\n", pkt.SequenceNumber)
			received++
			if received >= numPackets {
				close(done)
				return
			}

		}
	})

	assert.NoError(t, signalPair(pcOffer, pcAnswer))

	func() {
		for {
			select {
			case <-done:
				time.Sleep(300 * time.Millisecond)

				fmt.Println("\n---------------- STATS REPORT ----------------")

				// 1. Sender Stats (pcOffer)
				fmt.Println(">>> SENDER (Offer) Stats:")
				statsOffer := pcOffer.GetStats()
				for _, s := range statsOffer {
					// Wir filtern nach 'outbound-rtp', da dies die gesendeten Pakete betrifft
					if s.Type == webrtc.StatsTypeOutboundRTP {
						// Casten für detaillierten Zugriff (optional), oder einfach ausgeben
						stats, _ := s.(webrtc.OutboundRTPStreamStats)
						fmt.Printf("ID: %s | PacketsSent: %d | BytesSent: %d\n",
							stats.ID, stats.PacketsSent, stats.BytesSent)
					}
				}

				// 2. Receiver Stats (pcAnswer)
				fmt.Println("\n>>> RECEIVER (Answer) Stats:")
				statsAnswer := pcAnswer.GetStats()
				for _, s := range statsAnswer {
					// Wir filtern nach 'inbound-rtp', da dies die empfangenen Pakete betrifft
					if s.Type == webrtc.StatsTypeInboundRTP {
						stats, _ := s.(webrtc.InboundRTPStreamStats)
						fmt.Printf("ID: %s | PacketsReceived: %d | PacketsLost: %d | NackCount: %d | Jitter: %f\n",
							stats.ID, stats.PacketsReceived, stats.PacketsLost, stats.NackCount, stats.Jitter)
					}
				}
				fmt.Println("----------------------------------------------")

				return
			case <-time.After(20 * time.Millisecond):
				// sending sample data every 20 ms
				writeErr := track.WriteSample(media.Sample{Data: []byte{0x00}, Duration: time.Second})
				assert.NoError(t, writeErr)
			}
		}
	}()
}
