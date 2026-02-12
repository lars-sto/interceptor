// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeConfig_ClampDefaultsAndBounds(t *testing.T) {
	def := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 5,
		NumFECPackets:   2,
		CoverageMode:    CoverageModeInterleaved,
	}

	in := RuntimeConfig{
		Enabled:          true,
		NumMediaPackets:  0,   // should become 5
		NumFECPackets:    999, // should clamp to media
		CoverageMode:     "",  // should become interleaved
		InterleaveStride: 123, // should pass through
		BurstSpan:        7,   // should pass through
	}

	out := in.clamp(def)

	assert.Equal(t, uint32(5), out.NumMediaPackets)
	assert.Equal(t, uint32(5), out.NumFECPackets)
	assert.Equal(t, CoverageModeInterleaved, out.CoverageMode)
	assert.Equal(t, uint32(123), out.InterleaveStride)
	assert.Equal(t, uint32(7), out.BurstSpan)
}

func TestRuntimeConfig_ToActiveUsesClamp(t *testing.T) {
	def := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 10,
		NumFECPackets:   3,
		CoverageMode:    CoverageModeInterleaved,
	}

	in := RuntimeConfig{
		Enabled:         false,
		NumMediaPackets: 0,  // should take default
		NumFECPackets:   99, // should clamp to media=10
		CoverageMode:    "", // should take default
	}

	a := in.toActive(def)

	assert.Equal(t, false, a.enabled)
	assert.Equal(t, uint32(10), a.numMedia)
	assert.Equal(t, uint32(10), a.numFec)
	assert.Equal(t, CoverageModeInterleaved, a.coverageMode)
}
