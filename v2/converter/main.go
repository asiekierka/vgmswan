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

package main

import (
	_ "embed"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/asiekierka/vgmswan/v2/converter/vgm"
	"github.com/oov/audio/resampler"
)

var Emit0xFC = true
var Emit0xEF = true
var Enable24KHzSamples = false
var DisablePCM = false
var DisableResampling = false
var BuildTestROM = false
var OutputFilename = ""

//go:embed engine.bin
var engineBin []byte
var romSizeToHeaderValue = map[int]byte{
	128 * 1024:       0,
	256 * 1024:       1,
	512 * 1024:       2,
	1024 * 1024:      3,
	2 * 1024 * 1024:  4,
	4 * 1024 * 1024:  6,
	8 * 1024 * 1024:  8,
	16 * 1024 * 1024: 9,
}

type Sample struct {
	Data         *[]byte
	FilePosition uint32
	Frequency    uint32
}

type PCMSampleData struct {
	Data       []byte
	OrigOffset uint32
	OrigLength uint32
}

type DACStream struct {
	CtrlA, CtrlD uint8
	Frequency    uint32
}

type CommandWriteMemory struct {
	Address uint16
	Data    []byte
}

type CommandWritePort struct {
	Address byte
	Data    []byte
}

type CommandWait struct {
	Length uint32
}

type CommandPlaySample struct {
	Sample       *Sample
	CustomOffset uint16
	CustomLength uint16
	Repeat       bool
	Reverse      bool
}

type CommandJump struct {
	TargetSample uint32
}

type CommandFrame struct {
	Position  uint32
	Commands  []interface{}
	LoopFrame bool
}

type Song struct {
	Samples      []*Sample
	Commands     []*CommandFrame
	LoopPosition uint32
}

type BankData struct {
	Samples []*Sample
	Songs   []*Song
}

type ConvertedSampleKey struct {
	blockId   uint16
	frequency uint32
}

type ConvertedSampleMap struct {
	data map[ConvertedSampleKey]*Sample
}

func NewConvertedSampleMap() ConvertedSampleMap {
	c := ConvertedSampleMap{}
	c.data = make(map[ConvertedSampleKey]*Sample)
	return c
}

func (c *ConvertedSampleMap) ConvertSample(idx uint16, freq uint32, data PCMSampleData) (*Sample, bool) {
	key := ConvertedSampleKey{
		idx, freq,
	}
	if DisableResampling {
		key.frequency = 24000
		if !Enable24KHzSamples || freq <= 16000 {
			key.frequency = 12000
			if freq <= 8000 {
				key.frequency = 6000
				if freq <= 4800 {
					key.frequency = 4000
				}
			}
		}

		if sample, ok := c.data[key]; ok {
			return sample, false
		} else {
			sample := Sample{}
			sample.Frequency = key.frequency
			sample.Data = &data.Data
			c.data[key] = &sample
			return &sample, true
		}
	} else {
		if sample, ok := c.data[key]; ok {
			return sample, false
		} else {
			// resample
			sample := Sample{}
			sample.Frequency = 12000
			if freq <= 7000 {
				sample.Frequency = 6000
				if freq <= 4500 {
					sample.Frequency = 4000
				}
			}

			inputDataFloat := make([]float32, len(data.Data))
			for i, s := range data.Data {
				inputDataFloat[i] = (float32(s) - 127.5) / 127.5
			}
			outDataLen := int((uint64(len(data.Data)) * uint64(sample.Frequency)) / uint64(freq))
			outputDataFloat := make([]float32, outDataLen)
			resampler.Resample32(inputDataFloat, int(freq), outputDataFloat, int(sample.Frequency), 10)
			outputData := make([]byte, outDataLen)
			for i, s := range outputDataFloat {
				outputData[i] = byte((s * 127.5) + 127.5)
			}

			fmt.Printf("resampled sample %d: %d Hz(%d bytes) to %d hz(%d bytes)\n", idx, freq, len(data.Data), sample.Frequency, outDataLen)

			sample.Data = &outputData
			c.data[key] = &sample
			return &sample, true
		}
	}
}

var (
	ErrUnsupportedSongFile = errors.New("unsupported song file")
)

func parseVGM(r io.ReadSeeker) (*Song, error) {
	var song Song
	header, err := vgm.ReadVGMHeader(r)
	if err != nil {
		return nil, err
	}

	// TODO: more error checking?
	if header.ClockWonderSwan == 0 {
		return nil, ErrUnsupportedSongFile
	}

	var pcmSampleData []PCMSampleData
	convertedSamples := NewConvertedSampleMap()
	var dacStreams = make(map[uint8]*DACStream)
	var pcmSampleOffset uint32 = 0
	var samplePos uint32 = 0
	var newSamplePos uint32 = 0
	r.Seek(int64(header.DataOffset), io.SeekStart)
	running := true

	getDacStream := func(id uint8) *DACStream {
		if stream, ok := dacStreams[id]; ok {
			return stream
		} else {
			stream := DACStream{}
			dacStreams[id] = &stream
			return &stream
		}
	}

	getDacStreamReader := func() *DACStream {
		var id uint8
		binary.Read(r, binary.LittleEndian, &id)
		return getDacStream(id)
	}

	requestSampleReset := false
	frame := CommandFrame{}
	if newSamplePos == song.LoopPosition {
		frame.LoopFrame = true
	}
	for running {
		// check for loop offset
		filePos, err := r.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		if filePos == int64(header.LoopOffset) {
			requestSampleReset = true
			song.LoopPosition = samplePos
		}

		// parse command
		var lastCommand interface{} = nil
		if len(frame.Commands) > 0 {
			lastCommand = frame.Commands[len(frame.Commands)-1]
		}
		var cmd uint8
		if err := binary.Read(r, binary.LittleEndian, &cmd); err != nil {
			return nil, err
		}
		switch cmd {
		case 0x61:
			var length uint16
			binary.Read(r, binary.LittleEndian, &length)
			newSamplePos = samplePos + uint32(length)
		case 0x62:
			newSamplePos = samplePos + 735
		case 0x63:
			newSamplePos = samplePos + 882
		case 0x66:
			// end of file
			running = false
		case 0x67:
			// PCM stream data
			var pcmData PCMSampleData
			var length uint32
			var dataType uint8
			r.Seek(1, io.SeekCurrent) // skip 0x66
			binary.Read(r, binary.LittleEndian, &dataType)
			binary.Read(r, binary.LittleEndian, &length)

			origData := make([]byte, length)
			r.Read(origData)

			pcmData.Data = origData
			pcmData.OrigOffset = pcmSampleOffset
			pcmData.OrigLength = length

			pcmSampleOffset += length
			pcmSampleData = append(pcmSampleData, pcmData)
		case 0x90:
			stream := getDacStreamReader()
			r.Seek(1, io.SeekCurrent) // skip chip type
			binary.Read(r, binary.LittleEndian, &stream.CtrlA)
			binary.Read(r, binary.LittleEndian, &stream.CtrlD)
		case 0x91:
			// setup stream data, TODO
			r.Seek(4, io.SeekCurrent)
		case 0x92:
			stream := getDacStreamReader()
			binary.Read(r, binary.LittleEndian, &stream.Frequency)
		case 0x93:
			// start stream slow
			stream := getDacStreamReader()
			var offset uint32
			var flags uint8
			var length uint32
			binary.Read(r, binary.LittleEndian, &offset)
			binary.Read(r, binary.LittleEndian, &flags)
			binary.Read(r, binary.LittleEndian, &length)
			if !DisablePCM {
				cmd := CommandPlaySample{}
				found := false
				blockId := uint16(0)
				for i, block := range pcmSampleData {
					if offset >= block.OrigOffset && (offset+length) <= (block.OrigOffset+block.OrigLength) {
						found = true
						blockId = uint16(i)
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("could not find sample data for offset %d", offset)
				}
				sample, isNew := convertedSamples.ConvertSample(blockId, stream.Frequency, pcmSampleData[blockId])
				if isNew {
					song.Samples = append(song.Samples, sample)
				}
				sampleRatio := float64(1.0)
				if !DisableResampling {
					sampleRatio = float64(sample.Frequency) / float64(stream.Frequency)
				}
				cmd.Sample = sample
				cmd.CustomOffset = uint16(float64(offset-uint32(pcmSampleData[blockId].OrigOffset)) * sampleRatio)
				cmd.CustomLength = uint16(float64(length) * sampleRatio)
				switch flags & 0x03 {
				case 0:
				case 3:
				case 1:
					// TODO: "number of commands" not supported
					break
				case 2:
					length *= 44
				}
				cmd.Repeat = (flags & 0x80) != 0
				cmd.Reverse = (flags & 0x10) != 0
				frame.Commands = append(frame.Commands, &cmd)
			}
			requestSampleReset = true
		case 0x94:
			// stop stream
			if !DisablePCM {
				frame.Commands = append(frame.Commands, &CommandPlaySample{})
			}
			requestSampleReset = false
			r.Seek(1, io.SeekCurrent)
		case 0x95:
			// start stream fast
			stream := getDacStreamReader()
			var blockId uint16
			var flags uint8
			binary.Read(r, binary.LittleEndian, &blockId)
			binary.Read(r, binary.LittleEndian, &flags)
			if !DisablePCM {
				if int(blockId) >= len(pcmSampleData) {
					return nil, fmt.Errorf("missing PCM data block %d", blockId)
				}
				cmd := CommandPlaySample{}
				sample, isNew := convertedSamples.ConvertSample(blockId, stream.Frequency, pcmSampleData[blockId])
				if isNew {
					song.Samples = append(song.Samples, sample)
				}
				cmd.Sample = sample
				cmd.Repeat = (flags & 0x01) != 0
				cmd.Reverse = (flags & 0x10) != 0
				frame.Commands = append(frame.Commands, &cmd)
			}
			requestSampleReset = true
		case 0xBC:
			// WonderSwan write (I/O)
			var addr, data uint8
			binary.Read(r, binary.LittleEndian, &addr)
			binary.Read(r, binary.LittleEndian, &data)
			if addr == 0x0F || addr == 0x11 {
				// skip these!
				break
			}
			if !DisablePCM {
				if addr == 0x10 && (data&0x20) == 0 && (data&0x02) != 0 && requestSampleReset {
					// HACK: force stop sample here!
					// this shouldn't take so much space :(
					frame.Commands = append(frame.Commands, &CommandPlaySample{})
					requestSampleReset = false
				}
			}
			if cmd, ok := lastCommand.(*CommandWritePort); ok && len(cmd.Data) == 1 && (cmd.Address == addr-1 || cmd.Address == addr+1) {
				if cmd.Address == addr+1 {
					cmd.Data = []byte{data, cmd.Data[0]}
					cmd.Address -= 1
				} else {
					cmd.Data = append(cmd.Data, data)
				}
			} else {
				frame.Commands = append(frame.Commands, &CommandWritePort{
					addr, []byte{data},
				})
			}
		case 0xC6:
			// WonderSwan write (memory)
			var addr uint16
			var data uint8
			binary.Read(r, binary.BigEndian, &addr)
			binary.Read(r, binary.LittleEndian, &data)
			if addr >= 0x40 {
				return nil, fmt.Errorf("unsupported WonderSwan memory address: %04X", addr)
			}
			if cmd, ok := lastCommand.(*CommandWriteMemory); ok && cmd.Address == uint16(int(addr)-len(cmd.Data)) && (addr&0x0F) != 0 {
				cmd.Data = append(cmd.Data, data)
			} else {
				frame.Commands = append(frame.Commands, &CommandWriteMemory{
					addr, []byte{data},
				})
			}
		default:
			return nil, fmt.Errorf("unknown command %02X", cmd)
		}
		if newSamplePos > samplePos {
			// TODO: support vblank mode
			waitTime := ((newSamplePos-samplePos)*120 + 440) / 441
			if waitTime > 0 {
				frame.Commands = append(frame.Commands, &CommandWait{
					waitTime,
				})
				newFrame := frame
				song.Commands = append(song.Commands, &newFrame)
				frame = CommandFrame{}
				if newSamplePos == song.LoopPosition {
					frame.LoopFrame = true
				}
			}
			samplePos = newSamplePos
		}
	}

	return &song, nil
}

func writeBankPosition(w io.Writer, currentPosition uint32, writtenPosition uint32) error {
	pos := uint16(writtenPosition & 0xFFFF)
	bank := uint8(((writtenPosition & 0xFF0000) - (currentPosition & 0xFF0000)) >> 16)
	_, err := w.Write([]byte{uint8(pos), uint8(pos >> 8), bank})
	return err
}

func init() {
	flag.BoolVar(&DisablePCM, "disable-pcm", false, "Disable PCM samples.")
	flag.BoolVar(&DisableResampling, "disable-resampling", false, "Disable resampling.")
	flag.BoolVar(&Enable24KHzSamples, "enable-24khz-samples", false, "Enable 24kHz samples.")
	flag.BoolVar(&BuildTestROM, "t", false, "Output playback ROM.")
	flag.StringVar(&OutputFilename, "o", "", "Output filename.")
}

func main() {
	var data BankData

	flag.Parse()
	if flag.NArg() <= 0 {
		fmt.Fprintln(os.Stderr, "Please provide at least one song.")
		flag.Usage()
		os.Exit(1)
	}
	if len(OutputFilename) <= 0 {
		fmt.Fprintln(os.Stderr, "Please provide a valid output filename.")
		flag.Usage()
		os.Exit(1)
	}

	for _, songFilename := range flag.Args() {
		songReader, err := os.Open(songFilename)
		if err != nil {
			panic(err)
		}
		defer songReader.Close()

		song, err := parseVGM(songReader)
		if err != nil {
			panic(err)
		}
		data.Songs = append(data.Songs, song)
	}

	// deduplicate and populate samples
	sampleDedupMap := make(map[*Sample]*Sample)
	for _, song := range data.Songs {
		for i := 0; i < len(song.Samples); i++ {
			sample := song.Samples[i]
			found := false
			for _, otherSample := range data.Samples {
				if reflect.DeepEqual(sample, otherSample) {
					sampleDedupMap[otherSample] = sample
					found = true
					break
				}
			}
			if !found {
				sampleDedupMap[sample] = sample
				data.Samples = append(data.Samples, sample)
			}
		}
	}

	// emit song and sample data
	songWriter, err := os.Create(OutputFilename)
	if err != nil {
		panic(err)
	}
	defer songWriter.Close()

	position := uint32(0)
	// write empty song pointers for now
	for i := 0; i < len(data.Songs); i++ {
		songWriter.Write([]byte{0, 0, 0})
		position += 3
	}
	if BuildTestROM {
		songWriter.Write([]byte{0xFF, 0xFF, 0xFF})
		position += 3
	}
	if !DisablePCM {
		// write all sample data
		for i, sample := range data.Samples {
			found := false
			for j := 0; j < i; j++ {
				otherSample := data.Samples[j]
				if reflect.DeepEqual(sample.Data, otherSample.Data) {
					sample.FilePosition = otherSample.FilePosition
					found = true
					break
				}
			}
			if !found {
				sample.FilePosition = position
				if sample.FilePosition+uint32(len(*sample.Data)) > 65536 {
					panic(fmt.Errorf("sample data bank too big :-("))
				}
				songWriter.Write(*sample.Data)
				position += uint32(len(*sample.Data))
			}
		}
	}
	// start writing song data
	wavetableCache := make(map[[16]byte]uint16)
	frameCache := make([]*CommandFrame, 0)
	appendCmd := func(cmdBuffer []byte) {
		curBank := position >> 16
		nextBank := (position + uint32(len(cmdBuffer)) + 1) >> 16
		if curBank != nextBank {
			songWriter.Write([]byte{0xF7})
			position += 1
			for (position & 0xFFFF) != 0 {
				songWriter.Write([]byte{0xFF})
				position += 1
			}
			wavetableCache = make(map[[16]byte]uint16)
			frameCache = make([]*CommandFrame, 0)
		}
		songWriter.Write(cmdBuffer)
		filePos, _ := songWriter.Seek(0, io.SeekCurrent)
		position = uint32(filePos)
	}
	for i := 0; i < len(data.Songs); i++ {
		song := data.Songs[i]
		loopPosition := position
		songWriter.Seek(int64(i*3), io.SeekStart)
		writeBankPosition(songWriter, 0, position)
		songWriter.Seek(int64(position), io.SeekStart)

		for _, frame := range song.Commands {
			if frame.LoopFrame {
				filePos, _ := songWriter.Seek(0, io.SeekCurrent)
				loopPosition = uint32(filePos)
			}
			found := false
			if Emit0xEF {
				for _, otherFrame := range frameCache {
					if reflect.DeepEqual(otherFrame.Commands, frame.Commands) {
						found = true
						appendCmd([]byte{0xEF, uint8(otherFrame.Position), uint8(otherFrame.Position >> 8)})
						break
					}
				}
			}
			if !found {
				frame.Position = position
				frameCache = append(frameCache, frame)
				for _, cmdRaw := range frame.Commands {
					cmdBuffer := []byte{}
					switch cmd := cmdRaw.(type) {
					case *CommandWritePort:
						if len(cmd.Data) == 2 {
							cmdBuffer = append(cmdBuffer, append([]byte{0x60 + cmd.Address}, cmd.Data...)...)
						} else if len(cmd.Data) == 1 {
							cmdBuffer = append(cmdBuffer, append([]byte{0x40 + cmd.Address}, cmd.Data...)...)
						} else {
							panic(fmt.Errorf("unknown port write data length %+v", cmd))
						}
					case *CommandWriteMemory:
						if Emit0xFC && (cmd.Address&0x000F) == 0 && cmd.Address < 0x40 && len(cmd.Data) == 16 && (position&0xFFFF) < 0xFFE8 {
							key := *(*[16]byte)(cmd.Data)
							if pos, ok := wavetableCache[key]; ok {
								cmdBuffer = append(cmdBuffer, uint8(0xFC+(cmd.Address>>4)), uint8(pos), uint8(pos>>8))
								break
							} else {
								wavetableCache[key] = uint16(position + 2)
							}
						}
						cmdBuffer = append(cmdBuffer, append([]byte{uint8(cmd.Address), uint8(len(cmd.Data))}, cmd.Data...)...)
					case *CommandWait:
						if cmd.Length >= 256 {
							cmdBuffer = append(cmdBuffer, 0xF9, uint8(cmd.Length), uint8(cmd.Length>>8))
						} else if cmd.Length > 7 {
							cmdBuffer = append(cmdBuffer, 0xF8, uint8(cmd.Length))
						} else if cmd.Length > 0 {
							cmdBuffer = append(cmdBuffer, 0xEF+uint8(cmd.Length))
						}
					case *CommandPlaySample:
						if cmd.Sample == nil {
							cmdBuffer = append(cmdBuffer, 0xFB, 0x00)
						} else {
							ctrl := uint8(0x80)
							pos := uint16(cmd.Sample.FilePosition + uint32(cmd.CustomOffset))
							len := uint16(len(*cmd.Sample.Data))
							if cmd.CustomLength > 0 {
								len = cmd.CustomLength
							}
							if cmd.Reverse {
								pos += len - 1
								ctrl |= 0x40
							}
							if cmd.Repeat {
								ctrl |= 0x08
							}
							switch cmd.Sample.Frequency {
							case 4000:
								break
							case 6000:
								ctrl |= 0x01
							case 12000:
								ctrl |= 0x02
							case 24000:
								ctrl |= 0x03
							default:
								panic(fmt.Errorf("unknown frequency %d", cmd.Sample.Frequency))
							}
							cmdBuffer = append(cmdBuffer, 0xFB, ctrl, uint8(pos), uint8(pos>>8), uint8(len), uint8(len>>8))
						}
					default:
						panic(fmt.Errorf("unknown command type %+v", cmd))
					}
					appendCmd(cmdBuffer)
				}
			}
		}
		songWriter.Write([]byte{0xFA})
		writeBankPosition(songWriter, position, loopPosition)
		filePos, _ := songWriter.Seek(0, io.SeekCurrent)
		position = uint32(filePos)
	}

	if BuildTestROM {
		// read all written data so far (to calculate checksum)
		filePos, _ := songWriter.Seek(0, io.SeekCurrent)
		fileData := make([]byte, filePos)
		songWriter.Seek(0, io.SeekStart)
		songWriter.Read(fileData)
		checksum := uint16(0)
		for _, d := range fileData {
			checksum += uint16(d)
		}
		fileTargetSize := 131072
		for fileTargetSize < (len(engineBin) + len(fileData)) {
			fileTargetSize *= 2
		}
		engineBin[len(engineBin)-6] = romSizeToHeaderValue[fileTargetSize]

		// calculate checksum remainder
		for i := 0; i < len(engineBin)-2; i++ {
			checksum += uint16(engineBin[i])
		}
		padByte := []byte{0xFF}
		padByteCount := fileTargetSize - len(engineBin) - len(fileData)
		checksum += uint16(uint64(padByteCount) * uint64(padByte[0]))
		engineBin[len(engineBin)-2] = uint8(checksum)
		engineBin[len(engineBin)-1] = uint8(checksum >> 8)

		for i := 0; i < padByteCount; i++ {
			songWriter.Write(padByte)
		}
		songWriter.Write(engineBin)
	}
}
