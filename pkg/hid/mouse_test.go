package hid

import (
	"reflect"
	"testing"
)

func TestMouseReport_Bytes(t *testing.T) {
	r := &MouseReport{
		Buttons: 0x01, // Left click
		X:       10,
		Y:       -5,
		Wheel:   -1,
	}

	// -5 in two's complement int8 is 251 byte => 0xFB
	// -1 in two's complement int8 is 255 byte => 0xFF
	expected := []byte{0x01, 10, 251, 255}
	res := r.Bytes()

	if !reflect.DeepEqual(res, expected) {
		t.Errorf("Expected %v, got %v", expected, res)
	}
}
