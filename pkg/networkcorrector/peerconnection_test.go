package networkcorrector

import (
	"github.com/pion/webrtc/v4"
)

func signalPair(pcOffer *webrtc.PeerConnection, pcAnswer *webrtc.PeerConnection) error {
	return signalPairWithModification(
		pcOffer,
		pcAnswer,
		func(sessionDescription string) string { return sessionDescription },
	)
}

func signalPairWithModification(
	pcOffer *webrtc.PeerConnection,
	pcAnswer *webrtc.PeerConnection,
	modificationFunc func(string) string,
) error {
	// Note(albrow): We need to create a data channel in order to trigger ICE
	// candidate gathering in the background for the JavaScript/Wasm bindings. If
	// we don't do this, the complete offer including ICE candidates will never be
	// generated.
	if _, err := pcOffer.CreateDataChannel("initial_data_channel", nil); err != nil {
		return err
	}

	offer, err := pcOffer.CreateOffer(nil)
	if err != nil {
		return err
	}
	offerGatheringComplete := webrtc.GatheringCompletePromise(pcOffer)
	if err = pcOffer.SetLocalDescription(offer); err != nil {
		return err
	}
	<-offerGatheringComplete

	offer.SDP = modificationFunc(pcOffer.LocalDescription().SDP)
	if err = pcAnswer.SetRemoteDescription(offer); err != nil {
		return err
	}

	answer, err := pcAnswer.CreateAnswer(nil)
	if err != nil {
		return err
	}
	answerGatheringComplete := webrtc.GatheringCompletePromise(pcAnswer)
	if err = pcAnswer.SetLocalDescription(answer); err != nil {
		return err
	}
	<-answerGatheringComplete

	return pcOffer.SetRemoteDescription(*pcAnswer.LocalDescription())
}
