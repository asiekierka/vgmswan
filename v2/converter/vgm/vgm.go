// Copyright (c) 2022 Adrian Siekierka
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// VGM parsing helper for Go.
//
// TODO:
// - Extra Header support

package vgm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

const (
	VGM_SAMPLES_PER_SECOND      = 44100
	VGM_SN76489_FREQ_0_IS_0x400 = 0x01
	VGM_SN76489_OUTPUT_NEGATE   = 0x02
	VGM_SN76489_GAME_GEAR_MONO  = 0x04
	VGM_SN76489_CLOCK_DIV8_OFF  = 0x08
	VGM_SN76489_XNOR_NOISE      = 0x10
	VGM_YM2610_CLOCK_MASK       = 0x7FFFFFFF
	VGM_YM2610_IS_B_MODEL       = 0x80000000
	VGM_AY8910_TYPE_AY8910      = 0x00
	VGM_AY8910_TYPE_AY8912      = 0x01
	VGM_AY8910_TYPE_AY8913      = 0x02
	VGM_AY8910_TYPE_AY8930      = 0x03
	VGM_AY8910_TYPE_YM2149      = 0x10
	VGM_AY8910_TYPE_YM3439      = 0x11
	VGM_AY8910_TYPE_YMZ284      = 0x12
	VGM_AY8910_TYPE_YMZ294      = 0x13
	VGM_AY8910_LEGACY_OUTPUT    = 0x01
	VGM_AY8910_SINGLE_OUTPUT    = 0x02
	VGM_AY8910_DISCRETE_OUTPUT  = 0x04
	VGM_AY8910_RAW_OUTPUT       = 0x08
	VGM_N2A03_CLOCK_MASK        = 0x7FFFFFFF
	VGM_N2A03_FDS_PRESENT       = 0x80000000
	VGM_OKIM6258_CLOCK_DIV_MASK = 0x03
	VGM_OKIM6258_CLOCK_DIV_1024 = 0x00
	VGM_OKIM6258_CLOCK_DIV_768  = 0x01
	VGM_OKIM6258_CLOCK_DIV_512  = 0x02
	VGM_OKIM6258_CLOCK_DIV_512A = 0x03
	VGM_OKIM6258_ADPCM_3BIT     = 0x04
	VGM_OKIM6258_OUTPUT_12BIT   = 0x08
	VGM_K054539_REVERSE_STEREO  = 0x01
	VGM_K054539_DISABLE_REVERB  = 0x02
	VGM_K054539_UPDATE_AT_KEYON = 0x04
	VGM_C140_TYPE_NAMCO2        = 0x00
	VGM_C140_TYPE_NAMCO21       = 0x01
	VGM_C140_TYPE_NAMCO_NA12    = 0x02
	VGM_K051649_CLOCK_MASK      = 0x7FFFFFFF
	VGM_K051649_IS_K052539      = 0x80000000
	VGM_ES5505_CLOCK_MASK       = 0x7FFFFFFF
	VGM_ES5505_IS_ES5506        = 0x80000000
)

type VGMHeader struct {
	Ident                     [4]byte
	EofOffset                 uint32
	Version                   uint32
	ClockSN76489              uint32
	ClockYM2413               uint32
	OffsetGD3                 uint32
	SampleCount               uint32
	LoopOffset                uint32
	LoopSampleCount           uint32
	Rate                      uint32
	FeedbackSN76489           uint16
	ShiftRegisterWidthSN76489 uint8
	FlagsSN76489              uint8
	ClockYM2612               uint32
	ClockYM2151               uint32
	DataOffset                uint32
	ClockSegaPCM              uint32
	InterfaceRegisterSegaPCM  uint32
	ClockRF5C68               uint32
	ClockYM2203               uint32
	ClockYM2608               uint32
	ClockYM2610               uint32
	ClockYM3812               uint32
	ClockYM3526               uint32
	ClockY8950                uint32
	ClockYMF262               uint32
	ClockYMF278B              uint32
	ClockYMF271               uint32
	ClockYMZ280B              uint32
	ClockRF5C164              uint32
	ClockPWM                  uint32
	ClockAY8910               uint32
	TypeAY8910                uint8
	FlagsAY8910               uint8
	FlagsAY8910YM2203         uint8
	FlagsAY8910YM2608         uint8
	VolumeModifier            uint8
	_                         uint8
	LoopBase                  uint8
	LoopModifier              uint8
	ClockDMG                  uint32
	ClockN2A03                uint32
	ClockMultiPCM             uint32
	ClockUPD7759              uint32
	ClockOKIM6258             uint32
	FlagsOKIM6258             uint8
	FlagsK054539              uint8
	ChipTypeC140              uint8
	_                         uint8
	ClockOKIM6295             uint32
	ClockK051649              uint32
	ClockK054539              uint32
	ClockHUC6280              uint32
	ClockC140                 uint32
	ClockK053260              uint32
	ClockPokey                uint32
	ClockQSound               uint32
	ClockSCSP                 uint32
	ExtraHeaderOffset         uint32
	ClockWonderSwan           uint32
	ClockVSU                  uint32
	ClockSAA1099              uint32
	ClockES5503               uint32
	ClockES5505               uint32
	OutputChannelsES5503      uint8
	OutputChannelsES5505      uint8
	ClockDividerC352          uint8
	_                         uint8
	ClockX1010                uint32
	ClockC352                 uint32
	ClockGA20                 uint32
}

const (
	vgmMaxVersion = 0x171
)

var (
	vgmIdent                 = []byte{'V', 'g', 'm', ' '}
	ErrVGMInvalidHeader      = errors.New("invalid VGM header")
	ErrVGMUnsupportedVersion = errors.New("unsupported VGM version")
)

func ReadVGMHeader(r io.Reader) (*VGMHeader, error) {
	var header VGMHeader

	// Create a temporary buffer.
	buffer := make([]byte, binary.Size(&header))
	r.Read(buffer[0x00:0x24])
	ident := buffer[0:4]
	version := uint32(buffer[8]) | (uint32(buffer[9]) << 8) | (uint32(buffer[10]) << 16) | (uint32(buffer[11]) << 24)
	if !bytes.Equal(ident, vgmIdent) {
		return nil, ErrVGMInvalidHeader
	}
	if version < 0x100 || version > vgmMaxVersion {
		return nil, ErrVGMUnsupportedVersion
	}
	headerLength := 0x24
	if version >= 0x171 {
		headerLength = 0xE4
	} else if version >= 0x170 {
		headerLength = 0xC0
	} else if version >= 0x161 {
		headerLength = 0xB8
	} else if version >= 0x151 {
		headerLength = 0x80
	} else if version >= 0x150 {
		headerLength = 0x38
	} else if version >= 0x110 {
		headerLength = 0x34
	} else if version >= 0x101 {
		headerLength = 0x28
	}
	if headerLength > 0x24 {
		r.Read(buffer[0x24:headerLength])
	}

	err := binary.Read(bytes.NewReader(buffer), binary.LittleEndian, &header)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}

	// adjustments
	if header.LoopOffset == 0xFFFFFFFF {
		header.LoopOffset = 0
	} else if header.LoopOffset != 0 {
		header.LoopOffset += 0x1C
	}
	if version < 0x150 {
		header.DataOffset = 0x40
	} else {
		header.DataOffset += 0x34
	}
	if version < 0x171 {
		header.ClockSCSP = 0
	}
	if version < 0x160 {
		header.VolumeModifier = 0
		header.LoopBase = 0
	}
	if version < 0x151 {
		header.FlagsSN76489 = 0
	}
	if version >= 0x170 {
		if header.ExtraHeaderOffset != 0 {
			header.ExtraHeaderOffset += 0xBC
		}
	}

	return &header, nil
}
