// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

// RuntimeConfig is the runtime-applied FlexFEC behavior for one outbound media stream
// FlexFEC is active only when Enabled is true and both NumMediaPackets and NumFECPackets are greater than zero
type RuntimeConfig struct {
	Enabled bool

	NumMediaPackets uint32
	NumFECPackets   uint32
}

// Internal snapshot-type
type activeRuntimeConfig struct {
	enabled bool

	numMedia uint32
	numFec   uint32
}

// clamp performs minimal defensive normalization
func (c RuntimeConfig) clamp() RuntimeConfig {
	out := c

	if out.NumFECPackets > out.NumMediaPackets {
		out.NumFECPackets = out.NumMediaPackets
	}

	if out.NumMediaPackets > MaxMediaPackets {
		out.NumMediaPackets = MaxMediaPackets
	}

	if out.NumFECPackets > MaxFecPackets {
		out.NumFECPackets = MaxFecPackets
	}

	return out
}
