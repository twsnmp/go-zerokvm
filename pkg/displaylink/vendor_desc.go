package displaylink

import (
	"encoding/binary"
)

type DlVendorUsbDescriptor struct {
	MaxWidth  uint32
	MaxHeight uint32
	MaxPixels uint32
}

func NewVendorDescriptor(maxWidth, maxHeight uint16, maxPixels uint32) *DlVendorUsbDescriptor {
	return &DlVendorUsbDescriptor{
		MaxWidth:  uint32(maxWidth),
		MaxHeight: uint32(maxHeight),
		MaxPixels: maxPixels,
	}
}

func (d *DlVendorUsbDescriptor) Bytes() []byte {
	buf := make([]byte, 26)
	buf[0] = 26   // bLength
	buf[1] = 0x5f // bDescriptorType
	binary.LittleEndian.PutUint16(buf[2:4], 0x0001) // bcdVersion
	buf[4] = 24   // bLength2

	// MaxPixels
	binary.LittleEndian.PutUint16(buf[5:7], 0x0200) // Key
	buf[7] = 4                                      // Length
	binary.LittleEndian.PutUint32(buf[8:12], d.MaxPixels)

	// MaxWidth
	binary.LittleEndian.PutUint16(buf[12:14], 0x0201) // Key
	buf[14] = 4                                       // Length
	binary.LittleEndian.PutUint32(buf[15:19], d.MaxWidth)

	// MaxHeight
	binary.LittleEndian.PutUint16(buf[19:21], 0x0202) // Key
	buf[21] = 4                                       // Length
	binary.LittleEndian.PutUint32(buf[22:26], d.MaxHeight)

	return buf
}

func (d *DlVendorUsbDescriptor) ComputeChecksum() int32 {
	data := d.Bytes()[2:]
	var checksum int32 = 0
	for _, v := range data {
		checksum = (-(int32(v^byte(checksum)) & 1) & 0x101e) ^ checksum
		checksum = (-(int32((v>>1)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)
		checksum = (-(int32((v>>2)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)
		checksum = (-(int32((v>>3)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)
		checksum = (-(int32((v>>4)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)
		checksum = (-(int32((v>>5)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)
		checksum = (-(int32((v>>6)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)
		checksum = ((-(int32((v>>7)^byte(checksum>>1)) & 1) & 0x101e) ^ (checksum >> 1)) >> 1
	}
	return checksum
}
