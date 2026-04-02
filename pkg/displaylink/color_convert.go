//go:build cgo

package displaylink

/*
#include "color_convert.h"
*/
import "C"
import "unsafe"

func CopyRgb565BeToLe(source []uint16, destination []uint16) uint16 {
	if len(source) == 0 || len(destination) == 0 {
		return 0
	}
	count := len(source)
	if len(destination) < count {
		count = len(destination)
	}

	res := C.copy_rgb565_be_to_le(
		(*C.uint16_t)(unsafe.Pointer(&source[0])),
		(*C.uint16_t)(unsafe.Pointer(&destination[0])),
		C.size_t(count),
	)
	return uint16(res)
}

func CopyRgb565LeToRgbx(source []uint16, destination []uint32) {
	if len(source) == 0 || len(destination) == 0 {
		return
	}
	count := len(source)
	if len(destination) < count {
		count = len(destination)
	}

	C.copy_rgb565_le_to_rgbx(
		(*C.uint16_t)(unsafe.Pointer(&source[0])),
		(*C.uint32_t)(unsafe.Pointer(&destination[0])),
		C.size_t(count),
	)
}
