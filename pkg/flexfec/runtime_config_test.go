// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

package flexfec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeConfig_ClampClampsFECToMedia(t *testing.T) {
	in := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 4,
		NumFECPackets:   999,
	}

	out := in.clamp()

	assert.Equal(t, true, out.Enabled)
	assert.Equal(t, uint32(4), out.NumMediaPackets)
	assert.Equal(t, uint32(4), out.NumFECPackets)
}

func TestRuntimeConfig_ClampKeepsExplicitValues(t *testing.T) {
	in := RuntimeConfig{
		Enabled:         false,
		NumMediaPackets: 4,
		NumFECPackets:   1,
	}

	out := in.clamp()

	assert.Equal(t, false, out.Enabled)
	assert.Equal(t, uint32(4), out.NumMediaPackets)
	assert.Equal(t, uint32(1), out.NumFECPackets)
}

func TestRuntimeConfig_ClampKeepsZeroValues(t *testing.T) {
	in := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 0,
		NumFECPackets:   0,
	}

	out := in.clamp()

	assert.Equal(t, true, out.Enabled)
	assert.Equal(t, uint32(0), out.NumMediaPackets)
	assert.Equal(t, uint32(0), out.NumFECPackets)
}

func TestRuntimeConfig_ClampDoesNotChangeEqualMediaAndFEC(t *testing.T) {
	in := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: 5,
		NumFECPackets:   5,
	}

	out := in.clamp()

	assert.Equal(t, true, out.Enabled)
	assert.Equal(t, uint32(5), out.NumMediaPackets)
	assert.Equal(t, uint32(5), out.NumFECPackets)
}

func TestRuntimeConfig_ClampClampsMediaToMax(t *testing.T) {
	in := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: MaxMediaPackets + 10,
		NumFECPackets:   1,
	}

	out := in.clamp()

	assert.Equal(t, MaxMediaPackets, out.NumMediaPackets)
	assert.Equal(t, uint32(1), out.NumFECPackets)
}

func TestRuntimeConfig_ClampClampsFECToMax(t *testing.T) {
	in := RuntimeConfig{
		Enabled:         true,
		NumMediaPackets: MaxMediaPackets,
		NumFECPackets:   MaxFecPackets + 10,
	}

	out := in.clamp()

	assert.Equal(t, MaxMediaPackets, out.NumMediaPackets)
	assert.Equal(t, MaxMediaPackets, out.NumFECPackets)
}
