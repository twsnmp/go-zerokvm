package hid

import (
	"reflect"
	"testing"
)

func TestKeyboardReport_AddKey(t *testing.T) {
	r := &KeyboardReport{}

	// Add 1st key
	if !r.AddKey(0x04) {
		t.Errorf("Failed to add first key")
	}
	if r.Keys[0] != 0x04 {
		t.Errorf("Expected 0x04 at index 0, got 0x%02x", r.Keys[0])
	}

	// Add duplicate key
	if !r.AddKey(0x04) {
		t.Errorf("Failed to handle duplicate key insertion silently")
	}

	// Add more up to limit
	r.AddKey(0x05)
	r.AddKey(0x06)
	r.AddKey(0x07)
	r.AddKey(0x08)
	r.AddKey(0x09)

	// Add 7th key (should fail or ignore)
	if r.AddKey(0x0A) {
		t.Errorf("Should not be able to add 7th key")
	}

	expected := [6]byte{0x04, 0x05, 0x06, 0x07, 0x08, 0x09}
	if r.Keys != expected {
		t.Errorf("Expected %v, got %v", expected, r.Keys)
	}
}

func TestKeyboardReport_RemoveKey(t *testing.T) {
	r := &KeyboardReport{
		Keys: [6]byte{0x04, 0x05, 0x06, 0x07, 0x00, 0x00},
	}

	r.RemoveKey(0x05) // Remove middle key

	expected := [6]byte{0x04, 0x06, 0x07, 0x00, 0x00, 0x00}
	if r.Keys != expected {
		t.Errorf("Expected %v, got %v", expected, r.Keys)
	}

	r.RemoveKey(0x04) // Remove first key
	expected2 := [6]byte{0x06, 0x07, 0x00, 0x00, 0x00, 0x00}
	if r.Keys != expected2 {
		t.Errorf("Expected %v, got %v", expected2, r.Keys)
	}
}

func TestKeyboardReport_Bytes(t *testing.T) {
	r := &KeyboardReport{
		Modifiers: 0x02, // Shift
		Reserved:  0x00,
		Keys:      [6]byte{0x04, 0x05, 0x00, 0x00, 0x00, 0x00},
	}
	expected := []byte{0x02, 0x00, 0x04, 0x05, 0x00, 0x00, 0x00, 0x00}
	if !reflect.DeepEqual(r.Bytes(), expected) {
		t.Errorf("Expected %v, got %v", expected, r.Bytes())
	}
}
