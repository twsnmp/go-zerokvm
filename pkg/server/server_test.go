package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/twsnmp/go-zerokvm/pkg/displaylink"
)

type mockWriter struct {
	Captured []byte
}

func (m *mockWriter) Write(p []byte) (n int, err error) {
	m.Captured = append(m.Captured, p...)
	return len(p), nil
}

func TestServer_HandlePointer_BootMouse(t *testing.T) {
	mem := displaylink.NewMemory()
	assets := http.FS(fstest.MapFS{})

	relWriter := &mockWriter{}
	srv := NewServer(mem, assets, nil, nil, relWriter)

	x, y := 10.0, -5.0
	leftClick := true
	reqObj := PointerEventRequest{
		Type: "BootMouse",
		Events: []PointerEvent{
			{X: &x, Y: &y, Left: &leftClick},
		},
	}
	body, _ := json.Marshal(reqObj)

	req, _ := http.NewRequest("POST", "/api/pointer", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handlePointer(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("got %v want %v", rr.Code, http.StatusOK)
	}

	time.Sleep(50 * time.Millisecond) // wait for background goroutine to process channel

	// Expected BootsMouse report: Buttons(byte), dX(int8->byte), dY, dWheel
	// Buttons = 1 (Left), dX = 10, dY = -5 (251)
	expected := []byte{0x01, 10, 251, 0}
	if len(relWriter.Captured) < 4 || !bytes.Equal(relWriter.Captured[:4], expected) {
		t.Errorf("Expected BootMouse report %v, got %v", expected, relWriter.Captured)
	}
}

func TestServer_HandleKeyboard(t *testing.T) {
	mem := displaylink.NewMemory()
	assets := http.FS(fstest.MapFS{})

	kbWriter := &mockWriter{}
	srv := NewServer(mem, assets, kbWriter, nil, nil)

	reqObj := KeyboardEventRequest{
		Keys: []KeyboardKey{
			{ScanCode: 0x04, IsDown: true}, // 'A'
			{ScanCode: 225, IsDown: true},  // Left Shift (Modifier 0xE1)
		},
	}
	body, _ := json.Marshal(reqObj)

	req, _ := http.NewRequest("POST", "/api/keyboard", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleKeyboard(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("got %v want %v", rr.Code, http.StatusOK)
	}

	time.Sleep(50 * time.Millisecond) // wait for background goroutine to process channel

	// Modified byte: Left Shift is (225 - 224) = 1. 1<<1 = 0x02.
	// Key array = 0x04 0x00 ...
	expected := []byte{0x02, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00}
	if len(kbWriter.Captured) < 8 || !bytes.Equal(kbWriter.Captured[:8], expected) {
		t.Errorf("Expected Keyboard report %v, got %v", expected, kbWriter.Captured)
	}
}
