package functionfs

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

type Ep0 struct {
	file *os.File
}

func NewEp0(mountPath string) (*Ep0, error) {
	f, err := os.OpenFile(filepath.Join(mountPath, "ep0"), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return &Ep0{file: f}, nil
}

func (e *Ep0) Close() error {
	return e.file.Close()
}

func (e *Ep0) ReadEvents(buf []Event) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}
	size := int(unsafe.Sizeof(Event{}))
	b := (*[1 << 30]byte)(unsafe.Pointer(&buf[0]))[:len(buf)*size]
	n, err := e.file.Read(b)
	if err != nil {
		return 0, err
	}
	return n / size, nil
}

func (e *Ep0) ReadEvent() (*Event, error) {
	var ev Event
	size := int(unsafe.Sizeof(ev))
	b := (*[1 << 30]byte)(unsafe.Pointer(&ev))[:size]
	_, err := io.ReadFull(e.file, b)
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

func (e *Ep0) Write(data []byte) (int, error) {
	return e.file.Write(data)
}

func (e *Ep0) WriteDescriptors(fs, hs, ss [][]byte) error {
	var headerLen uint32 = 12
	var countsLen uint32 = 0
	var dataLen uint32 = 0
	var flags DescsFlags = AllCtrlRecip | Config0Setup

	if len(fs) > 0 {
		flags |= HasFullSpeedDesc
		countsLen += 4
		for _, d := range fs {
			dataLen += uint32(len(d))
		}
	}
	if len(hs) > 0 {
		flags |= HasHighSpeedDesc
		countsLen += 4
		for _, d := range hs {
			dataLen += uint32(len(d))
		}
	}
	if len(ss) > 0 {
		flags |= HasSuperSpeedDesc
		countsLen += 4
		for _, d := range ss {
			dataLen += uint32(len(d))
		}
	}

	totalLen := headerLen + countsLen + dataLen
	buf := make([]byte, totalLen)
	
	// Header
	binary.LittleEndian.PutUint32(buf[0:4], DescriptorsMagic)
	binary.LittleEndian.PutUint32(buf[4:8], totalLen)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(flags))

	// Counts
	offset := int(headerLen)
	if len(fs) > 0 {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(fs)))
		offset += 4
	}
	if len(hs) > 0 {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(hs)))
		offset += 4
	}
	if len(ss) > 0 {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(ss)))
		offset += 4
	}

	// Data
	writeGroup := func(ds [][]byte) {
		for _, d := range ds {
			copy(buf[offset:], d)
			offset += len(d)
		}
	}

	writeGroup(fs)
	writeGroup(hs)
	writeGroup(ss)

	_, err := e.file.Write(buf)
	return err
}

func (e *Ep0) WriteStrings(lang uint16, strs []string) error {
	var stringsLen uint32 = 0
	for _, s := range strs {
		stringsLen += uint32(len(s)) + 1
	}

	totalLen := uint32(unsafe.Sizeof(StringsHead{})) + 2 + stringsLen
	buf := make([]byte, totalLen)
	head := StringsHead{
		Magic:     StringsMagic,
		Length:    totalLen,
		StrCount:  uint32(len(strs)),
		LangCount: 1,
	}
	head.Write(buf)

	binary.LittleEndian.PutUint16(buf[16:18], lang)
	offset := 18
	for _, s := range strs {
		copy(buf[offset:], s)
		offset += len(s)
		buf[offset] = 0
		offset++
	}

	_, err := e.file.Write(buf)
	return err
}

func (e *Ep0) ReadExactly(buf []byte) error {
	if len(buf) == 0 {
		// Force a syscall to acknowledge 0-length OUT transfers in FunctionFS.
		// io.ReadFull would skip this.
		_, _, errno := syscall.Syscall(syscall.SYS_READ, e.file.Fd(), 0, 0)
		if errno == 0 {
			return nil
		}
		return errno
	}
	_, err := io.ReadFull(e.file, buf)
	return err
}
