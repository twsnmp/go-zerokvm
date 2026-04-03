// Package displaylink provides a decoder and memory model for the DisplayLink USB graphics protocol.
package displaylink

import (
	"encoding/binary"
	"sync"
	"sync/atomic"

	"github.com/twsnmp/go-zerokvm/pkg/logger"
)

type RgbColorDepth byte

const (
	Rgb8Bits  RgbColorDepth = 0x00
	Rgb16Bits RgbColorDepth = 0x01
	Rgb24Bits RgbColorDepth = 0x02
)

const (
	MaxPixels  = 1152 * 2048
	RAMSize    = 64 * 1024
	FBPhysSize = 32 * 1024 * 1024
	FBSize     = FBPhysSize + 256
)

type Memory struct {
	Mu          sync.RWMutex
	UpdateCount uint64 // Atomic counter incremented on every draw (MUST BE 8-BYTE ALIGNED)
	RAM         []byte
	FrameBuffer []byte
	
	HorizontalResolution uint32
	VerticalResolution   uint32
	FB16BaseOffset       uint32
	FB16LineStride       uint32
	FB8BaseOffset        uint32
	FB8LineStride        uint32
	
	ActiveOffset         uint32
	BlankOutput          bool
	
	ColorDepth RgbColorDepth
	
	DecompTable8Lookup  []DecompLookupEntry
	DecompTable8Colors  []byte
	DecompTable16Lookup []DecompLookupEntry
	DecompTable16Colors []uint16
}

type DecompLookupEntry struct {
	ColorCount uint16
	Jump       uint16
}

func (e DecompLookupEntry) IsSet() bool {
	return e.ColorCount != 0 || e.Jump != 0
}

func NewMemory() *Memory {
	m := &Memory{
		RAM:         make([]byte, RAMSize),
		FrameBuffer: make([]byte, FBSize),
	}
	// Initial fill with a default color (some blue/green pattern)
	// We intentionally leave RAM[0xc484/0xc485] at zero (as C# code did) to prevent 
	// Windows 11 from misinterpreting a "1" as a standby directive.
	// Initial fill with a default color (some blue/green pattern)
	for i := 0; i < len(m.FrameBuffer); i += 2 {
		binary.LittleEndian.PutUint16(m.FrameBuffer[i:i+2], 0b0000011111100000)
	}
	return m
}

func (m *Memory) SetRegister(address byte, value byte) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.SetRegisterInternal(address, value)
}

func (m *Memory) SetRegisterInternal(address byte, value byte) {
	const Offset = 0xc300
	m.RAM[Offset+int(address)] = value
	
	// Temporary debug: log significant register writes
	if address >= 0x0f && address <= 0x30 || address == 0xff || address == 0x1f {
		logger.Debugf("Register Write: Addr=0x%02x, Val=0x%02x (Total: 0x%x 0x%x 0x%x 0x%x)", 
			address, value, m.RAM[Offset+0x20], m.RAM[Offset+0x21], m.RAM[Offset+0x22], m.RAM[Offset+0x23])
	}

	// If registers update flag is non-zero, apply changes immediately
	if m.RAM[Offset+0xff] != 0 {
		m.applyRegistersInternal()
	}
}

func (m *Memory) applyRegistersInternal() {
	const Offset = 0xc300
	r := m.RAM[Offset:]
	
	// Register addresses from DlRegisterAddress.cs
	// HPixels = 0x0f (16-bit BE)
	// VPixels = 0x17 (16-bit BE)
	// BaseOffset16 = 0x20 (24-bit BE)
	// LineStride16 = 0x23 (24-bit BE)
	
	hRes := uint32(binary.BigEndian.Uint16(r[0x0f:0x11]))
	vRes := uint32(binary.BigEndian.Uint16(r[0x17:0x19]))
	
	m.HorizontalResolution = hRes
	m.VerticalResolution = vRes
	
	m.FB16BaseOffset = uint32(readUint24Be(r[0x20:0x23]))
	stride16 := readUint24Be(r[0x23:0x26])
	if stride16 == 0 {
		stride16 = int(hRes * 2)
	}
	m.FB16LineStride = uint32(stride16)
	
	m.FB8BaseOffset = uint32(readUint24Be(r[0x26:0x29]))
	stride8 := readUint24Be(r[0x29:0x2c])
	if stride8 == 0 {
		m.FB8LineStride = m.HorizontalResolution
	} else {
		m.FB8LineStride = uint32(stride8)
	}
	
	m.ColorDepth = RgbColorDepth(r[0x00]) // ColorDepth = 0x00
	newBlank := r[0x1f] != 0
	if newBlank != m.BlankOutput {
		logger.Debugf("Blank state changed: %v -> %v", m.BlankOutput, newBlank)
		m.BlankOutput = newBlank
	}

	logger.Debugf("Registers applied: %dx%d, Depth: %d, FB16 Offset: 0x%x, Stride: %d, Blank: %v",
		m.HorizontalResolution, m.VerticalResolution, m.ColorDepth, m.FB16BaseOffset, m.FB16LineStride, m.BlankOutput)

	// Notify server that display parameters have changed
	atomic.AddUint64(&m.UpdateCount, 1)
}

func readUint24Be(b []byte) int {
	return (int(b[0]) << 16) | (int(b[1]) << 8) | int(b[2])
}
