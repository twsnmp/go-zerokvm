//go:build !cgo

package displaylink

func CopyRgb565BeToLe(source []uint16, destination []uint16) uint16 {
	if len(source) == 0 || len(destination) == 0 {
		return 0
	}
	count := len(source)
	if len(destination) < count {
		count = len(destination)
	}

	var pixel uint16
	for i := 0; i < count; i++ {
		val := source[i]
		pixel = (val >> 8) | (val << 8)
		destination[i] = pixel
	}
	return pixel
}

func CopyRgb565LeToRgbx(source []uint16, destination []uint32) {
	if len(source) == 0 || len(destination) == 0 {
		return
	}
	count := len(source)
	if len(destination) < count {
		count = len(destination)
	}

	for i := 0; i < count; i++ {
		val := source[i]
		// Convert R5G6B5 to R8G8B8x8 (rough shift for generic version)
		r := uint32((val >> 11) & 0x1F) << 19
		g := uint32((val >> 5) & 0x3F) << 10
		b := uint32(val & 0x1F) << 3
		destination[i] = r | g | b | 0xFF000000
	}
}
