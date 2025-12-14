package networkcorrector

import (
	"github.com/pion/interceptor"
)

// NoOp is an Interceptor that does not modify any packets. It can embedded in other interceptors, so it's
// possible to implement only a subset of the methods.
type networkcorrector struct{}

// BindRTCPReader lets you modify any incoming RTCP packets. It is called once per sender/receiver, however this might
// change in the future. The returned method will be called once per packet batch.
func (i *networkcorrector) BindRTCPReader(reader interceptor.RTCPReader) interceptor.RTCPReader {
	return reader
}

// BindRTCPWriter lets you modify any outgoing RTCP packets. It is called once per PeerConnection. The returned method
// will be called once per packet batch.
func (i *networkcorrector) BindRTCPWriter(writer interceptor.RTCPWriter) interceptor.RTCPWriter {
	return writer
}

// BindLocalStream lets you modify any outgoing RTP packets. It is called once for per LocalStream. The returned method
// will be called once per rtp packet.
func (i *networkcorrector) BindLocalStream(_ *interceptor.StreamInfo, writer interceptor.RTPWriter) interceptor.RTPWriter {
	return writer
}

// UnbindLocalStream is called when the Stream is removed. It can be used to clean up any data related to that track.
func (i *networkcorrector) UnbindLocalStream(_ *interceptor.StreamInfo) {}

// BindRemoteStream lets you modify any incoming RTP packets.
// It is called once for per RemoteStream. The returned method
// will be called once per rtp packet.
func (i *networkcorrector) BindRemoteStream(_ *interceptor.StreamInfo, reader interceptor.RTPReader) interceptor.RTPReader {
	return reader
}

// UnbindRemoteStream is called when the Stream is removed. It can be used to clean up any data related to that track.
func (i *networkcorrector) UnbindRemoteStream(_ *interceptor.StreamInfo) {}

// Close closes the Interceptor, cleaning up any data if necessary.
func (i *networkcorrector) Close() error {
	return nil
}
