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
	waitTime   = 1500 // wait time in milliseconds before stats are being collected
)

func TestNetworkcorrector(t *testing.T) {
	interceptorRegistry := &interceptor.Registry{}

	mediaEngine := &webrtc.MediaEngine{}
	err := mediaEngine.RegisterDefaultCodecs()
	assert.NoError(t, err)

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

	rtcpSeen := make(chan struct{})

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

			rtcpSeen <- struct{}{}

			for _, p := range ps {
				switch pkt := p.(type) {

				case *rtcp.TransportLayerNack:
					fmt.Printf("Received RTCP NACK Packet %s\n", pkt)
				
				case *rtcp.ReceiverReport:
					for _, rr := range pkt.Reports {
						fmt.Printf("RTCP RR: ssrc=%d lost=%d jitter=%d\n",
							rr.SSRC, rr.TotalLost, rr.Jitter)
					}

				case *rtcp.SenderReport:
					fmt.Printf("RTCP SR: ssrc=%d ntp=%v rtpTime=%d\n",
						pkt.SSRC, pkt.NTPTime, pkt.RTPTime)

				case *rtcp.PictureLossIndication:
					fmt.Printf("RTCP PLI: mediaSSRC=%d\n", pkt.MediaSSRC)

				case *rtcp.TransportLayerCC:
					fmt.Printf("RTCP TWCC: \n")

				default:
					fmt.Printf("RTCP other: %T\n", pkt)
				}
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
				select {
				case <-rtcpSeen:
					// 1) Sender Stats (pcOffer)
					fmt.Println(">>> SENDER (Offer) Stats:")
					statsOffer := pcOffer.GetStats()
					for id, stat := range statsOffer {
						switch s := stat.(type) {
						case webrtc.OutboundRTPStreamStats:
							fmt.Printf("id(mapKey): %s | ID: %s | SSRC: %d | Kind: %s | PacketsSent: %d "+
								"| BytesSent: %d\n",
								id, s.ID, s.SSRC, s.Kind, s.PacketsSent, s.BytesSent)
						}
					}

					// 2) Receiver Stats (pcAnswer)
					fmt.Println("\n>>> RECEIVER (Answer) Stats:")
					statsAnswer := pcAnswer.GetStats()
					for id, stat := range statsAnswer {
						switch s := stat.(type) {
						case webrtc.InboundRTPStreamStats:
							fmt.Printf("id(mapKey): %s | ID: %s | SSRC: %d | Kind: %s | PacketsReceived: %d "+
								"| PacketsLost: %d | Jitter: %f | NACKCount: %d\n",
								id, s.ID, s.SSRC, s.Kind, s.PacketsReceived, s.PacketsLost, s.Jitter, s.NACKCount)
						}
					}
					fmt.Println("----------------------------------------------")

					return
				case <-time.After(waitTime * time.Millisecond):
					t.Fatal("timed out waiting for RTCP sender report")
				}

				fmt.Println("\n---------------- STATS REPORT ----------------")

			case <-time.After(20 * time.Millisecond):
				// sending sample data every 20 ms
				writeErr := track.WriteSample(media.Sample{Data: []byte{0x00}, Duration: time.Second})
				assert.NoError(t, writeErr)
			}
		}
	}()
}
