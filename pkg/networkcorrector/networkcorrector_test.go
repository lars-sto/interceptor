package networkcorrector

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/transport/v3/vnet"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/stretchr/testify/assert"
)

const (
	numPackets      = 200
	dropProbability = 0.5
)

func TestNetworkcorrector(t *testing.T) {
	interceptorRegistry := &interceptor.Registry{}

	mediaEngine := &webrtc.MediaEngine{}
	err := mediaEngine.RegisterDefaultCodecs()
	assert.NoError(t, err)

	err = webrtc.ConfigureFlexFEC03(49, mediaEngine, interceptorRegistry)
	assert.NoError(t, err)

	nc := NewFactory()
	interceptorRegistry.Add(nc)

	err = webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry)
	assert.NoError(t, err)

	pcOffer, pcAnswer, wan := createVNetPair(t, interceptorRegistry)

	wan.AddChunkFilter(func(c vnet.Chunk) bool {
		h := &rtp.Header{}
		if _, err := h.Unmarshal(c.UserData()); err != nil {
			return true
		}

		return randomDrop(dropProbability)
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
			rtpSender.Read(rtcpBuf)
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
			if received%50 == 0 {
				fmt.Printf("[Receiver] received packet no. %d (seq=%d)\n", received, pkt.SequenceNumber)
			}
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
				return
			case <-time.After(20 * time.Millisecond):
				// sending sample data every 20 ms
				writeErr := track.WriteSample(media.Sample{Data: []byte{0x00}, Duration: time.Second})
				assert.NoError(t, writeErr)
			}
		}
	}()
}
