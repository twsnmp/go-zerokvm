// Package displaylink handles the decoding of DisplayLink USB protocol
// and manages the internal framebuffer representation.
package displaylink


import (
	"encoding/binary"
	"log"
	"sync/atomic"

	"github.com/twsnmp/go-zerokvm/pkg/logger"
)

const CommandHeader = 0xaf

type Command uint16


const (
	CmdSetRegister      Command = CommandHeader | (0x20 << 8)
	CmdWrite8           Command = CommandHeader | (0x60 << 8)
	CmdWrite16          Command = CommandHeader | (0x68 << 8)
	CmdFill8            Command = CommandHeader | (0x61 << 8)
	CmdFill16           Command = CommandHeader | (0x69 << 8)
	CmdCopy8            Command = CommandHeader | (0x62 << 8)
	CmdCopy16           Command = CommandHeader | (0x6a << 8)
	CmdWriteRlx8        Command = CommandHeader | (0x63 << 8)
	CmdWriteRlx16       Command = CommandHeader | (0x6b << 8)
	CmdWriteComp8       Command = CommandHeader | (0x70 << 8)
	CmdWriteComp16      Command = CommandHeader | (0x78 << 8)
	CmdFlushPipe        Command = CommandHeader | (0xa0 << 8)
	CmdLoadDecompTable  Command = CommandHeader | (0xe0 << 8)
	CmdNoOp             Command = CommandHeader | (CommandHeader << 8)
	CmdTrailingZero     Command = CommandHeader << 8
	CmdTrailingDoubleZero Command = 0
)

const LookupBitCount = 8

type Decoder struct {
	memory *Memory
	buf    []byte
}

func NewDecoder(memory *Memory) *Decoder {
	return &Decoder{
		memory: memory,
		buf:    make([]byte, 0, 65536),
	}
}

func (d *Decoder) Decode(data []byte) error {
	d.buf = append(d.buf, data...)
	processed, _ := Process(d.buf, d.memory)
	if processed > 0 {
		if processed >= len(d.buf) {
			d.buf = d.buf[:0]
		} else {
			d.buf = d.buf[processed:]
		}
	}
	return nil
}

func uint24BeLsbToInt32(val uint32) uint32 {
	return (uint32(byte(val)) << 16) | (uint32(byte(val >> 8)) << 8) | uint32(byte(val >> 16))
}

func wrap256(val int) int {
	if val == 0 {
		return 256
	}
	return val & 0xff
}

func isValidSubCommand(sub byte) bool {
	switch sub {
	case 0x20, 0x60, 0x68, 0x61, 0x69, 0x62, 0x6a, 0x63, 0x6b, 0x70, 0x78, 0xa0, 0xe0, 0xaf:
		return true
	}
	return false
}

func Process(commandStream []byte, memory *Memory) (int, bool) {
	memory.Mu.Lock()
	defer memory.Mu.Unlock()
	
	dirty := false
	if len(commandStream) < 2 {
		return 0, false
	}

	offset := 0
	for offset <= len(commandStream)-2 {
		header := binary.LittleEndian.Uint16(commandStream[offset : offset+2])
		cmdData := commandStream[offset+2:]
		cmdLen := 0

		switch Command(header) {
		case CmdWrite8, CmdWrite16, CmdWriteRlx8, CmdWriteRlx16, CmdWriteComp8, CmdWriteComp16:
			switch Command(header) {
			case CmdWriteRlx8:
				cmdLen = processWriteRlx8(cmdData, memory)
			case CmdWriteRlx16:
				cmdLen = processWriteRlx16(cmdData, memory)
			case CmdWrite8:
				cmdLen = processWrite8(cmdData, memory)
			case CmdWrite16:
				cmdLen = processWrite16(cmdData, memory)
			case CmdWriteComp8:
				cmdLen = processWriteComp8(cmdData, memory)
			case CmdWriteComp16:
				cmdLen = processWriteComp16(cmdData, memory)
			}
			dirty = true
		case CmdFill8, CmdFill16:
			if Command(header) == CmdFill8 {
				cmdLen = processFill8(cmdData, memory)
			} else {
				cmdLen = processFill16(cmdData, memory)
			}
			dirty = true
		case CmdCopy8, CmdCopy16:
			if Command(header) == CmdCopy8 {
				cmdLen = processCopy8(cmdData, memory)
			} else {
				cmdLen = processCopy16(cmdData, memory)
			}
			dirty = true
		case CmdSetRegister:
			logger.Debugf("Set Register command at offset %d", offset)
			cmdLen = processSetRegister(cmdData, memory)
		case CmdLoadDecompTable:
			logger.Debugf("Load Decompression Table at offset %d", offset)
			cmdLen = processLoadDecompTable(cmdData, memory)
		case CmdFlushPipe, CmdTrailingDoubleZero:
			offset += 2
			atomic.AddUint64(&memory.UpdateCount, 1)
			continue
		case CmdNoOp, CmdTrailingZero:
			offset += 1
			continue
		default:
			// If we don't recognize the command, scan forward for a VALID next header pattern
			// A valid pattern is 0xaf followed by a known sub-command byte.
			found := false
			for i := offset + 1; i+2 <= len(commandStream); i++ {
				if commandStream[i] == CommandHeader && isValidSubCommand(commandStream[i+1]) {
					log.Printf("Desync detected at byte %d (header: %02x%02x). Resyncing to byte %d", 
						offset, commandStream[offset], commandStream[offset+1], i)
					offset = i
					found = true
					break
				}
			}
			if !found {
				// No valid header found in the rest of this buffer
				// Keep last byte in case it's the start of a header
				return len(commandStream) - 1, dirty
			}
			continue
		}

		if cmdLen == 0 {
			// Incomplete command, wait for more data
			return offset, dirty
		}
		offset += 2 + cmdLen
	}

	if dirty {
		atomic.AddUint64(&memory.UpdateCount, 1)
	}
	return offset, dirty
}

func processSetRegister(data []byte, memory *Memory) int {
	if len(data) < 2 { return 0 }
	address := data[0]
	value := data[1]
	memory.SetRegisterInternal(address, value)
	return 2
}

func processWrite8(data []byte, memory *Memory) int {
	if len(data) < 4 {
		return 0
	}
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	count := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer

	if len(data) < 4+count {
		return 0
	}

	for i := 0; i < count; i++ {
		fb[(address+uint32(i)) & (FBPhysSize-1)] = data[4+i]
	}

	return 4 + count
}

func processWrite16(data []byte, memory *Memory) int {
	if len(data) < 4 {
		return 0
	}
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	count := wrap256(int(header >> 24))

	fb := memory.FrameBuffer

	if len(data) < 4+(count*2) {
		return 0
	}

	for i := 0; i < count; i++ {
		val := binary.LittleEndian.Uint16(data[4+i*2 : 4+i*2+2])
		val = (val >> 8) | (val << 8) // swap BE to LE
		binary.LittleEndian.PutUint16(fb[(address+uint32(i*2)) & (FBPhysSize-1) : (address+uint32(i*2)) & (FBPhysSize-1)+2], val)
	}

	return 4 + count*2
}

func processFill8(data []byte, memory *Memory) int {
	if len(data) < 4 { return 0 }
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	total := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer

	offset := 4
	for total > 0 {
		if offset + 2 > len(data) { return 0 }
		count := wrap256(int(data[offset]))
		val := data[offset+1]
		for i := 0; i < count; i++ {
			fb[(address+uint32(i)) & (FBPhysSize-1)] = val
		}
		offset += 2
		address += uint32(count)
		total -= count
	}
	return offset
}

func processFill16(data []byte, memory *Memory) int {
	if len(data) < 4 { return 0 }
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	total := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer

	offset := 4
	for total > 0 {
		if offset + 3 > len(data) { return 0 }
		count := wrap256(int(data[offset]))
		val := binary.LittleEndian.Uint16(data[offset+1 : offset+3])
		for i := 0; i < count; i++ {
			binary.LittleEndian.PutUint16(fb[(address+uint32(i*2)) & (FBPhysSize-1) : (address+uint32(i*2)) & (FBPhysSize-1)+2], val)
		}
		offset += 3
		address += uint32(count * 2)
		total -= count
	}
	return offset
}

func processCopy8(data []byte, memory *Memory) int {
	if len(data) < 7 { return 0 }
	// Parse 24-bit Big-Endian addresses
	rawT := binary.LittleEndian.Uint32(data[:4])
	target := uint24BeLsbToInt32(rawT)
	count := wrap256(int(data[3]))
	
	// We only need 3 bytes for Source, but reading 4 bytes is how original code did it via uint32 read.
	// To be safe, we check if we have 7 bytes (which we do if len(data) >= 7),
	// but reading index 7 might be out of bounds if len(data) == 7.
	// However, uint24BeLsbToInt32 only uses indices 4, 5, 6.
	source := (uint32(data[4]) << 16) | (uint32(data[5]) << 8) | uint32(data[6])
	
	memory.ActiveOffset = target
	fb := memory.FrameBuffer

	for i := 0; i < count; i++ {
		fb[(target+uint32(i)) & (FBPhysSize-1)] = fb[(source+uint32(i)) & (FBPhysSize-1)]
	}
	return 7
}

func processCopy16(data []byte, memory *Memory) int {
	if len(data) < 7 { return 0 }
	rawT := binary.LittleEndian.Uint32(data[:4])
	target := uint24BeLsbToInt32(rawT)
	count := wrap256(int(data[3]))
	source := (uint32(data[4]) << 16) | (uint32(data[5]) << 8) | uint32(data[6])
	
	memory.ActiveOffset = target
	fb := memory.FrameBuffer

	for i := 0; i < count; i++ {
		val := binary.LittleEndian.Uint16(fb[(source+uint32(i*2)) & (FBPhysSize-1) : (source+uint32(i*2)) & (FBPhysSize-1)+2])
		binary.LittleEndian.PutUint16(fb[(target+uint32(i*2)) & (FBPhysSize-1) : (target+uint32(i*2)) & (FBPhysSize-1)+2], val)
	}
	return 7
}

type decompEntry struct {
	color ushort
	jump  ushort
}

type ushort = uint16

func processLoadDecompTable(data []byte, memory *Memory) int {
	if len(data) < 8 { return 0 }
	length := int(binary.BigEndian.Uint16(data[6:8]))
	
	totalLen := 8 + (length * 9)
	if len(data) < totalLen { return 0 }
	
	table := make([]decompEntry, length * 2)
	payload := data[8:]
	for i := 0; i < length; i++ {
		entryOffset := i * 9
		// Bit-packed 9-byte structure: 
		// Node A: 0-1 (colorA), 3 (jumpA high 5 bits), 4 (jumpA low 4 bits)
		// Node B: 5-6 (colorB), 8 (jumpB high 5 bits), 4 (jumpB low 4 bits)
		
		colorA := binary.BigEndian.Uint16(payload[entryOffset : entryOffset+2])
		jumpA := (uint16(payload[entryOffset+3])&0x1f)<<4 | (uint16(payload[entryOffset+4]) >> 4)
		
		colorB := binary.BigEndian.Uint16(payload[entryOffset+5 : entryOffset+7])
		jumpB := (uint16(payload[entryOffset+8])&0x1f)<<4 | (uint16(payload[entryOffset+4]) & 0xf)
		
		table[i*2] = decompEntry{color: colorA, jump: jumpA}
		table[i*2+1] = decompEntry{color: colorB, jump: jumpB}
	}
	
	// Pre-allocation of lookup table
	numLookup := (length * 2) * (1 << LookupBitCount)
	memory.DecompTable8Lookup = make([]DecompLookupEntry, numLookup)
	memory.DecompTable8Colors = make([]byte, numLookup*LookupBitCount)
	memory.DecompTable16Lookup = make([]DecompLookupEntry, numLookup)
	memory.DecompTable16Colors = make([]uint16, numLookup*LookupBitCount)
	
	buildTableLookup(table, memory.DecompTable8Lookup, memory.DecompTable8Colors, 0, 0)
	buildTableLookup(table, memory.DecompTable16Lookup, memory.DecompTable16Colors, 8, 8) 
	
	return totalLen
}

func buildTableLookup[T byte | uint16](table []decompEntry, tableLookup []DecompLookupEntry, tableColors []T, tableIndex uint32, startIndex uint32) {
	subLookupIdx := int(tableIndex) * (1 << LookupBitCount)
	if tableLookup[subLookupIdx].IsSet() {
		return
	}
	
	subColorsIdx := int(tableIndex) * (1 << LookupBitCount) * LookupBitCount
	for i := 0; i < (1 << LookupBitCount); i++ {
		entry, acc := lookup(table, tableColors[subColorsIdx+i*LookupBitCount : subColorsIdx+(i+1)*LookupBitCount], tableIndex, startIndex, uint32(i), LookupBitCount)
		tableLookup[subLookupIdx+i] = entry
		if entry.Jump != 0 && uint32(entry.Jump) != startIndex {
			buildTableLookup(table, tableLookup, tableColors, uint32(entry.Jump), startIndex)
		}
		_ = acc // used in lookup to fill the colors span
	}
}

func lookup[T byte | uint16](table []decompEntry, colors []T, tableIndex uint32, startIndex uint32, bits uint32, bitCount uint32) (DecompLookupEntry, uint32) {
	var accumulator uint32
	colorCount := 0
	for {
		entry := table[(tableIndex<<1) + (bits&1)]
		accumulator += uint32(entry.color)
		tableIndex = uint32(entry.jump)
		bits >>= 1
		bitCount--
		
		if tableIndex == 0 {
			colors[colorCount] = T(accumulator)
			colorCount++
			tableIndex = startIndex
		}
		
		if bitCount == 0 {
			break
		}
	}
	for i := colorCount; i < LookupBitCount; i++ {
		colors[i] = T(accumulator)
	}
	return DecompLookupEntry{ColorCount: uint16(colorCount), Jump: uint16(tableIndex)}, accumulator
}

func processWriteComp8(data []byte, memory *Memory) int {
	if len(data) < 4 { return 0 }
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	pixelCount := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer
	offset := 4
	
	tableIndex := uint16(0)
	var accumulator byte
	for pixelCount > 0 && offset < len(data) {
		bits := data[offset]
		offset++
		
		lookupIdx := (uint32(tableIndex) << LookupBitCount) | uint32(bits)
		entry := memory.DecompTable8Lookup[lookupIdx]
		
		colors := memory.DecompTable8Colors[lookupIdx*LookupBitCount : (lookupIdx+1)*LookupBitCount]
		drawCount := int(entry.ColorCount)
		if drawCount > pixelCount { drawCount = pixelCount }
		
		for i := 0; i < drawCount; i++ {
			fb[(address) & (FBPhysSize-1)] = colors[i] + accumulator
			address++
		}
		
		// In C#, the accumulator for 8-bit is also updated by the last color's absolute value
		if drawCount > 0 {
			accumulator = colors[drawCount-1] + accumulator
		}
		
		pixelCount -= drawCount
		tableIndex = entry.Jump
	}
	return offset
}

func processWriteComp16(data []byte, memory *Memory) int {
	if len(data) < 4 { return 0 }
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	pixelCount := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer
	offset := 4
	
	tableIndex := uint16(8) // Start index for 16-bit
	var accumulator uint16
	for pixelCount > 0 && offset < len(data) {
		bits := data[offset]
		offset++
		
		lookupIdx := (uint32(tableIndex) << LookupBitCount) | uint32(bits)
		entry := memory.DecompTable16Lookup[lookupIdx]
		
		colors := memory.DecompTable16Colors[lookupIdx*LookupBitCount : (lookupIdx+1)*LookupBitCount]
		drawCount := int(entry.ColorCount)
		if drawCount > pixelCount { drawCount = pixelCount }
		
		for i := 0; i < drawCount; i++ {
			pixel := colors[i] + accumulator
			binary.LittleEndian.PutUint16(fb[(address) & (FBPhysSize-1) : (address) & (FBPhysSize-1)+2], pixel)
			address += 2
		}
		
		// In C#, the accumulator is ALWAYS updated by the 8th pixel in the lookup entry
		// (which is EntryColorsVector.GetElement(7)) to ensure the sum for the next 8 bits is correct.
		accumulator = colors[LookupBitCount-1] + accumulator
		
		pixelCount -= drawCount
		tableIndex = entry.Jump
	}
	return offset
}

func processWriteRlx16(data []byte, memory *Memory) int {
	if len(data) < 4 {
		return 0
	}
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	total := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer
	offset := 4
	
	for total > 0 {
		if offset+1 > len(data) { return 0 }
		chunk := wrap256(int(data[offset]))
		offset++
		
		if offset+(chunk*2) > len(data) { return 0 }
		for i := 0; i < chunk; i++ {
			val := binary.LittleEndian.Uint16(data[offset : offset+2])
			val = (val >> 8) | (val << 8)
			offset += 2
			binary.LittleEndian.PutUint16(fb[(address) & (FBPhysSize-1) : (address) & (FBPhysSize-1)+2], val)
			address += 2
		}
		total -= chunk
	}
	return offset
}

func processWriteRlx8(data []byte, memory *Memory) int {
	if len(data) < 4 {
		return 0
	}
	header := binary.LittleEndian.Uint32(data[:4])
	address := uint24BeLsbToInt32(header)
	memory.ActiveOffset = address
	total := wrap256(int(header >> 24))
	
	fb := memory.FrameBuffer
	offset := 4
	
	for total > 0 {
		if offset+1 > len(data) { return 0 }
		chunk := wrap256(int(data[offset]))
		offset++
		
		if offset+chunk > len(data) { return 0 }
		for i := 0; i < chunk; i++ {
			fb[(address+uint32(i)) & (FBPhysSize-1)] = data[offset+i]
		}
		offset += chunk
		address += uint32(chunk)
		total -= chunk
		
		if total > 0 {
			if offset+1 > len(data) { return 0 }
			repeat := int(data[offset])
			offset++
			if repeat > 0 {
				last := data[offset-2]
				for i := 0; i < repeat; i++ {
					fb[(address+uint32(i)) & (FBPhysSize-1)] = last
				}
				address += uint32(repeat)
				total -= repeat
			}
		}
	}
	return offset
}
