// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

// CoverageMode describes how repair packets cover media packets within a batch
// v1: only Interleaved is implemented by the current encoder/coverage generator
// Other modes are reserved for future work
type CoverageMode string

const (
	CoverageModeInterleaved CoverageMode = "interleaved"
	CoverageModeContiguous  CoverageMode = "contiguous" // reserved
	CoverageModeHybrid      CoverageMode = "hybrid"     // reserved
	CoverageModeWeighted    CoverageMode = "weighted"   // reserved
)

// RuntimeConfig is the runtime-applied FlexFEC behavior for one outbound media stream (SSRC)
// This is intentionally small and "apply-ready": the interceptor does not interpret it
type RuntimeConfig struct {
	Enabled bool

	NumMediaPackets uint32
	NumFECPackets   uint32

	CoverageMode CoverageMode

	// Reserved knobs for future coverage strategies (not enforced in v1).
	InterleaveStride uint32
	BurstSpan        uint32
}

// Internal snapshot-type
type activeRuntimeConfig struct {
	enabled bool

	numMedia uint32
	numFec   uint32

	coverageMode     CoverageMode
	interleaveStride uint32
	burstSpan        uint32
}

// clamp performs minimal defensive normalization
func (c RuntimeConfig) clamp(defaults RuntimeConfig) RuntimeConfig {
	out := c

	// Preserve defaults if caller provides zeros
	if out.NumMediaPackets == 0 {
		out.NumMediaPackets = defaults.NumMediaPackets
	}

	// Clamp fec <= media
	if out.NumFECPackets > out.NumMediaPackets {
		out.NumFECPackets = out.NumMediaPackets
	}

	// Default to interleaved if empty
	if out.CoverageMode == "" {
		out.CoverageMode = defaults.CoverageMode
	}

	return out
}

func (c RuntimeConfig) toActive(defaults RuntimeConfig) activeRuntimeConfig {
	c = c.clamp(defaults)
	return activeRuntimeConfig{
		enabled:          c.Enabled,
		numMedia:         c.NumMediaPackets,
		numFec:           c.NumFECPackets,
		coverageMode:     c.CoverageMode,
		interleaveStride: c.InterleaveStride,
		burstSpan:        c.BurstSpan,
	}
}
