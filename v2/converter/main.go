package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/asiekierka/vgmswan/v2/converter/vgm"
)

type Sample struct {
	Data         []byte
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
	Length            uint32
	NewSamplePosition uint32
}

type CommandPlaySample struct {
	Sample  *Sample
	Repeat  bool
	Reverse bool
}

type CommandJump struct {
	TargetSample uint32
}

type Song struct {
	Samples      []*Sample
	Commands     []interface{}
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
	// TODO: add resampling...
	key.frequency = 12000
	if sample, ok := c.data[key]; ok {
		return sample, false
	} else {
		// TODO: add resampling
		sample := Sample{}
		sample.Data = data.Data
		sample.Frequency = 12000
		c.data[key] = &sample
		return &sample, true
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
		if len(song.Commands) > 0 {
			lastCommand = song.Commands[len(song.Commands)-1]
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
		case 0x94:
			// stop stream
			song.Commands = append(song.Commands, &CommandPlaySample{})
			r.Seek(1, io.SeekCurrent)
		case 0x95:
			// start stream fast
			stream := getDacStreamReader()
			var blockId uint16
			var flags uint8
			binary.Read(r, binary.LittleEndian, &blockId)
			binary.Read(r, binary.LittleEndian, &flags)
			if int(blockId) >= len(pcmSampleData) {
				return nil, fmt.Errorf("missing PCM data block %d", blockId)
			}
			cmd := CommandPlaySample{}
			sample, isNew := convertedSamples.ConvertSample(blockId, stream.Frequency, pcmSampleData[blockId])
			cmd.Sample = sample
			if isNew {
				song.Samples = append(song.Samples, sample)
			}
			cmd.Repeat = (flags & 0x01) != 0
			cmd.Reverse = (flags & 0x04) != 0
			song.Commands = append(song.Commands, &cmd)
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
			if addr == 0x10 && (data&0x20) == 0 && (data&0x02) != 0 && requestSampleReset {
				// HACK: force stop sample here!
				// this shouldn't take so much space :(
				song.Commands = append(song.Commands, &CommandPlaySample{})
				requestSampleReset = false
			}
			if cmd, ok := lastCommand.(*CommandWritePort); ok && len(cmd.Data) == 1 && (cmd.Address == addr-1 || cmd.Address == addr+1) {
				if cmd.Address == addr+1 {
					cmd.Data = []byte{data, cmd.Data[0]}
					cmd.Address -= 1
				} else {
					cmd.Data = append(cmd.Data, data)
				}
			} else {
				song.Commands = append(song.Commands, &CommandWritePort{
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
				song.Commands = append(song.Commands, &CommandWriteMemory{
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
				song.Commands = append(song.Commands, &CommandWait{
					waitTime, newSamplePos,
				})
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

func main() {
	var data BankData

	for _, songFilename := range os.Args[1:] {
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
	songWriter, err := os.Create("output.bin")
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
	// write all sample data
	for _, sample := range data.Samples {
		sample.FilePosition = position
		if sample.FilePosition+uint32(len(sample.Data)) > 65536 {
			panic(fmt.Errorf("sample data bank too big :-("))
		}
		songWriter.Write(sample.Data)
		position += uint32(len(sample.Data))
	}
	// start writing song data
	for i := 0; i < len(data.Songs); i++ {
		song := data.Songs[i]
		loopPosition := position
		songWriter.Seek(int64(i*3), io.SeekStart)
		writeBankPosition(songWriter, 0, position)
		songWriter.Seek(int64(position), io.SeekStart)
		for _, cmdRaw := range song.Commands {
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
				cmdBuffer = append(cmdBuffer, append([]byte{uint8(cmd.Address), uint8(len(cmd.Data))}, cmd.Data...)...)
			case *CommandWait:
				if cmd.Length >= 256 {
					cmdBuffer = append(cmdBuffer, 0xF9, uint8(cmd.Length), uint8(cmd.Length>>8))
				} else if cmd.Length > 7 {
					cmdBuffer = append(cmdBuffer, 0xF8, uint8(cmd.Length))
				} else if cmd.Length > 0 {
					cmdBuffer = append(cmdBuffer, 0xEF+uint8(cmd.Length))
				}
				if cmd.NewSamplePosition == song.LoopPosition {
					filePos, _ := songWriter.Seek(0, io.SeekCurrent)
					loopPosition = uint32(filePos)
				}
			case *CommandPlaySample:
				if cmd.Sample == nil {
					cmdBuffer = append(cmdBuffer, 0xFB, 0x00)
				} else {
					ctrl := uint8(0x80)
					pos := uint16(cmd.Sample.FilePosition)
					len := uint16(len(cmd.Sample.Data))
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
			curBank := position >> 16
			nextBank := (position + uint32(len(cmdBuffer)) + 1) >> 16
			if curBank != nextBank {
				songWriter.Write([]byte{0xF7})
				position += 1
				for (position & 0xFFFF) != 0 {
					songWriter.Write([]byte{0xFF})
					position += 1
				}
			}
			songWriter.Write(cmdBuffer)
			filePos, _ := songWriter.Seek(0, io.SeekCurrent)
			position = uint32(filePos)
		}
		songWriter.Write([]byte{0xFA})
		writeBankPosition(songWriter, position, loopPosition)
	}
}
