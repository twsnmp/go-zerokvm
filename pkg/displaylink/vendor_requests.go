package displaylink

import (
	"encoding/binary"
	"log"

	"github.com/twsnmp/go-zerokvm/pkg/logger"
	"github.com/twsnmp/go-zerokvm/pkg/usb/functionfs"
)

// Vendor Request Constants
const (
	RequestReadEdid       = 0x02
	RequestWriteRAM       = 0x03
	RequestReadRAM        = 0x04
	RequestVerifyChecksum = 0x05
	RequestGetDeviceFlags = 0x06
	RequestSetEncryption  = 0x12
)

// Default EDID data (128 bytes)
// Header: 00 FF FF FF FF FF FF 00
var defaultEdid = []byte{
	0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x04, 0x72, 0x22, 0x02, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x14, 0x01, 0x03, 0x80, 0x22, 0x1B, 0x78, 0xEA, 0x3E, 0xC5, 0xA2, 0x57, 0x4A, 0x9C, 0x25,
	0x13, 0x50, 0x54, 0xA5, 0x4B, 0x00, 0x81, 0x80, 0xA9, 0x40, 0x71, 0x4F, 0x01, 0x01, 0x01, 0x01,
	0x01, 0x01, 0x01, 0x01, 0x01, 0x01, 0x64, 0x19, 0x00, 0x40, 0x41, 0x00, 0x26, 0x30, 0x18, 0x88,
	0x36, 0x00, 0x54, 0x10, 0x11, 0x00, 0x00, 0x18, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x47, 0x65, 0x6E,
	0x65, 0x72, 0x69, 0x63, 0x0A, 0x20, 0x20, 0x20, 0x20, 0x20, 0x00, 0x00, 0x00, 0xFC, 0x00, 0x5A,
	0x65, 0x72, 0x6F, 0x4B, 0x56, 0x4D, 0x20, 0x56, 0x47, 0x41, 0x0A, 0x20, 0x00, 0x00, 0x00, 0xFD,
	0x00, 0x32, 0x4B, 0x1E, 0x53, 0x0B, 0x00, 0x0A, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x00, 0x6E,
}

func init() {
	// Dynamically ensure checksum for the literal
	var sum byte
	for i := 0; i < 127; i++ {
		sum += defaultEdid[i]
	}
	defaultEdid[127] = byte((256 - int(sum)%256) % 256)
}

// HandleVendorRequest processes DisplayLink-specific control requests on ep0.
func HandleVendorRequest(ep0 *functionfs.Ep0, request functionfs.UsbCtrlRequest, mem *Memory) {
	logger.Debugf("Setup Request: Type=0x%02x, Req=0x%02x, Val=0x%04x, Idx=0x%04x, Len=%d",
		request.RequestType, request.Request, request.Value, request.Index, request.Length)

	// GET_DESCRIPTOR 0x5f
	if request.RequestType == 0x80 && request.Request == 0x06 && (request.Value>>8) == 0x5f {
		logger.Debugln("Handling GET_DESCRIPTOR 0x5f (1024x768)")
		vDesc := NewVendorDescriptor(1024, 768, 1024*768)
		ep0.Write(vDesc.Bytes())
		return
	}

	if (request.RequestType & 0x80) != 0 { // IN (Device to Host)
		switch request.Request {
		case RequestReadEdid:
			// C# logic: offset is ReverseEndianness(Value). Status (0=OK, 1=Error) at resp[0].
			offset := (request.Value >> 8) | (request.Value << 8)

			resp := make([]byte, request.Length)
			if int(offset) > len(defaultEdid) || int(request.Length)-1 > len(defaultEdid) || int(offset)+int(request.Length)-1 > len(defaultEdid) {
				log.Printf("EDID Read error: offset=0x%04x, requestLength=%d, edidLen=%d", offset, request.Length, len(defaultEdid))
				resp[0] = 1 // Error
			} else {
				resp[0] = 0 // Success
				if request.Length > 1 {
					copy(resp[1:], defaultEdid[int(offset):int(offset)+int(request.Length)-1])
				}
			}
			logger.Debugf("EDID Read [0x%04x]: status=%d, len=%d, data[:4]=%x", offset, resp[0], request.Length, resp[:min(4, len(resp))])
			ep0.Write(resp)

		case RequestReadRAM:
			offset := int(request.Index)
			mem.Mu.Lock()
			// Status register 0xc484: set to 1 (connected) to satisfy host driver
			if offset == 0xc484 {
				mem.RAM[0xc484] = 1
				mem.RAM[0xc485] = 1 // Some drivers check the adjacent byte as well
			}
			data := make([]byte, request.Length)
			if offset+int(request.Length) <= len(mem.RAM) {
				copy(data, mem.RAM[offset:offset+int(request.Length)])
			}
			mem.Mu.Unlock()
			logger.Debugf("RAM Read [0x%04x]: val=%x, len=%d", offset, data, request.Length)
			ep0.Write(data)

		case RequestVerifyChecksum:
			// Must match the primary detailed timing of the EDID -> 1024x768
			vDesc := NewVendorDescriptor(1024, 768, 1024*768)
			checksum := vDesc.ComputeChecksum()
			resp := make([]byte, 4)
			binary.LittleEndian.PutUint32(resp, uint32(checksum))
			ep0.Write(resp)

		case RequestGetDeviceFlags:
			resp := make([]byte, 4)
			// 0xF0005000 is perfectly acceptable to macOS and avoids 5s reset loops
			// Bit 1 or Bit 0 (0x3) often signals "Monitor attached, poll interrupt EP"
			// Windows 11 also accepts 0xF0005000 when no vendor descriptor is used.
			logger.Debugln("Get device flags: 0xF0005000")
			binary.LittleEndian.PutUint32(resp, 0xF0005000)
			ep0.Write(resp)

		default:
			log.Printf("Unknown Vendor IN request: 0x%02x (Len=%d)", request.Request, request.Length)
			// Must provide full Length or host may timeout/reset
			resp := make([]byte, request.Length)
			ep0.Write(resp)
		}
	} else { // OUT (Host to Device)
		switch request.Request {
		case RequestWriteRAM:
			data := make([]byte, request.Length)
			if err := ep0.ReadExactly(data); err == nil {
				offset := int(request.Index)
				mem.Mu.Lock()
				if offset+len(data) <= len(mem.RAM) {
					copy(mem.RAM[offset:], data)
					// If writing to the register area (0xc300-0xc3ff), apply it
					if offset < 0xc400 && offset+len(data) > 0xc300 {
						mem.applyRegistersInternal()
					}
				}
				mem.Mu.Unlock()
				logger.Debugf("RAM Write [0x%04x]: val=%x, len=%d", offset, data, request.Length)
			}
		case RequestSetEncryption:
			data := make([]byte, request.Length)
			if err := ep0.ReadExactly(data); err == nil {
				logger.Debugf("Received Encryption Key: %x", data)
			}
		case 0x14: // Pulse/Keepalive
			data := make([]byte, request.Length)
			if err := ep0.ReadExactly(data); err != nil {
				// Don't treat interrupted system call (EINTR) as a fatal error
				logger.Debugf("EP0 OUT 0x14 result: %v", err)
			}
		default:
			log.Printf("Unknown Vendor OUT request: 0x%02x (Len=%d)", request.Request, request.Length)
			data := make([]byte, request.Length)
			if err := ep0.ReadExactly(data); err != nil {
				log.Printf("Error completing unknown OUT: %v", err)
			}
		}
	}
}
