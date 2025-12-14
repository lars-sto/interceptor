package networkcorrector

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/transport/v3/vnet"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/stretchr/testify/assert"
)

func TestNetworkcorrector(t *testing.T) {
	pcOffer, pcAnswer, wan := createVNetPair(t, nil)

	wan.AddChunkFilter(func(c vnet.Chunk) bool {
		h := &rtp.Header{}
		if _, err := h.Unmarshal(c.UserData()); err != nil {
			return true
		}

		// logic for packets drops comes here

		return true
	})

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000}, "video", "pion")
	assert.NoError(t, err)

	rtpSender, err := pcOffer.AddTrack(track)
	assert.NoError(t, err)

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
		for {
			pkt, _, readRTPErr := track.ReadRTP()
			if errors.Is(readRTPErr, io.EOF) {
				return
			} else if pkt.PayloadType == 0 {
				_ = fmt.Sprintf("[Receiver] received packet (seq=%d)", pkt.SequenceNumber)
				continue
			}
		}
	})

	assert.NoError(t, signalPair(pcOffer, pcAnswer))

	func() {
		for {
			select {
			case <-time.After(20 * time.Millisecond):
				// sending sample data every 20 ms
				writeErr := track.WriteSample(media.Sample{Data: []byte{0x00}, Duration: time.Second})
				assert.NoError(t, writeErr)
			}
		}
	}()
}
