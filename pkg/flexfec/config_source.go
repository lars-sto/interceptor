// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

// StreamKey identifies a local outbound media stream
type StreamKey struct {
	MediaSSRC uint32
}

// ConfigSource provides runtime config updates for FlexFEC
type ConfigSource interface {
	Subscribe(key StreamKey, fn func(cfg RuntimeConfig)) (unsubscribe func())
}
