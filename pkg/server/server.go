// Package server provides an MJPEG streaming server for ZeroKVM.
package server

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sync"
	"sync/atomic"
	"time"

	"github.com/twsnmp/go-zerokvm/pkg/displaylink"
)

// Server handles MJPEG streaming and HID (keyboard/mouse) event processing.
type Server struct {
	captureCounter uint32
	memory         *displaylink.Memory
	assets         http.FileSystem
	mu             sync.Mutex
	kbWriter       io.Writer
	relWriter      io.Writer
	absWriter      io.Writer

	keyState      []uint16
	keyStateMutex sync.Mutex
	mouseBtnState byte

	// Non-blocking HID channels
	kbChan       chan []byte
	mouseChan    chan []byte
	absMouseChan chan []byte
}

func (s *Server) startHidWorker(writer io.Writer, ch chan []byte, name string) {
	for report := range ch {
		if writer == nil {
			continue
		}
		_, err := writer.Write(report)
		if err != nil {
			log.Printf("HID %s write failed: %v", name, err)
		}
	}
}

// NewServer initializes a new Server with shared memory and assets.
func NewServer(memory *displaylink.Memory, assets http.FileSystem, kbWriter, absWriter, relWriter io.Writer) *Server {
	s := &Server{
		memory:       memory,
		assets:       assets,
		keyState:     make([]uint16, 0, 6),
		kbChan:       make(chan []byte, 100),
		mouseChan:    make(chan []byte, 100),
		absMouseChan: make(chan []byte, 100),
		kbWriter:     kbWriter,
		relWriter:    relWriter,
		absWriter:    absWriter,
	}

	// Start HID workers
	go s.startHidWorker(s.kbWriter, s.kbChan, "Keyboard")
	go s.startHidWorker(s.absWriter, s.absMouseChan, "AbsMouse")
	go s.startHidWorker(s.relWriter, s.mouseChan, "RelMouse")

	return s
}

// Start begins the HTTP server on the specified address.
func (s *Server) Start(addr string) error {
	// API handlers
	http.HandleFunc("/api/screen/mjpeg", s.handleMjpeg)
	http.HandleFunc("/api/screen/rects", s.handleRects)
	http.HandleFunc("/api/pointer", s.handlePointer)
	http.HandleFunc("/api/keyboard", s.handleKeyboard)

	// Shortcuts
	http.HandleFunc("/mjpeg", s.handleMjpeg)
	http.HandleFunc("/mjpeg+rects", s.handleRects)
	http.HandleFunc("/kvm/screen.mjpeg", s.handleRects) // Use rects for frontend

	// KVM APIs
	http.HandleFunc("/kvm/pointer", s.handlePointer)
	http.HandleFunc("/kvm/keyboard", s.handleKeyboard)
	http.HandleFunc("/kvm/keyboard/leds", s.handleKeyboardLeds)
	http.HandleFunc("/kvm/usb/state", s.handleUsbState)
	http.HandleFunc("/kvm/usb/attach", s.handleUsbState)
	http.HandleFunc("/kvm/usb/detach", s.handleUsbState)
	http.HandleFunc("/kvm/events", s.handleEvents)

	// Static files
	fs := http.FileServer(s.assets)
	http.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/kvm/viewer.html", http.StatusFound)
			return
		}

		// If the request is for /kvm/..., try serving it from the root of staticPath
		// effectively letting it work both with and without the /kvm/ prefix
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/kvm/" {
			// Check if it's an API call that should have been handled by HandleFunc
			// This is a safety net
			if r.URL.Path == "/kvm/screen.mjpeg" {
				s.handleRects(w, r)
				return
			}
			// Strip /kvm prefix
			r.URL.Path = r.URL.Path[4:]
		}

		fs.ServeHTTP(w, r)
	}))

	return http.ListenAndServe(addr, nil)
}

func (s *Server) handleKeyboardLeds(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"numLock":false,"capsLock":false,"scrollLock":false,"compose":false,"kana":false}`))
}

func (s *Server) handleUsbState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"attached":true}`))
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	queueID := r.URL.Query().Get("queueId")
	if queueID == "" {
		// Frontend needs a queueId to start polling
		w.Write([]byte(`{"queueId":"0"}`))
		return
	}

	// Long polling for events
	// Wait a bit to avoid busy loop
	time.Sleep(1 * time.Second)
	w.WriteHeader(http.StatusNoContent)
}

// PointerEventRequest represents a request for one or more pointer (mouse/touch) events.
type PointerEventRequest struct {
	Type   string         `json:"type"`
	Events []PointerEvent `json:"events"`
	Reset  bool           `json:"reset"`
}

// PointerEvent represents a single mouse button state change or movement event.
type PointerEvent struct {
	Left   *bool    `json:"left"`
	Middle *bool    `json:"middle"`
	Right  *bool    `json:"right"`
	X      *float64 `json:"x"`
	Y      *float64 `json:"y"`
	Wheel  *float64 `json:"wheel"`
}

// KeyboardEventRequest represents a request for one or more keyboard scan codes.
type KeyboardEventRequest struct {
	Keys  []KeyboardKey `json:"keys"`
	Reset bool          `json:"reset"`
}

// KeyboardKey represents a single keyboard key's scan code and its down/up state.
type KeyboardKey struct {
	ScanCode int  `json:"scanCode"`
	IsDown   bool `json:"isDown"`
}

func (s *Server) handlePointer(w http.ResponseWriter, r *http.Request) {
	var req PointerEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if atomic.AddUint32(&s.captureCounter, 1)%10 == 0 { // reuse counter for rate limiting
		log.Printf("Pointer Request: Type=%s, Events=%d, absWriter=%v", req.Type, len(req.Events), s.absWriter != nil)
	}

	for _, ev := range req.Events {
		if ev.Left != nil {
			if *ev.Left {
				s.mouseBtnState |= 0x01
			} else {
				s.mouseBtnState &= ^byte(0x01)
			}
		}
		if ev.Right != nil {
			if *ev.Right {
				s.mouseBtnState |= 0x02
			} else {
				s.mouseBtnState &= ^byte(0x02)
			}
		}
		if ev.Middle != nil {
			if *ev.Middle {
				s.mouseBtnState |= 0x04
			} else {
				s.mouseBtnState &= ^byte(0x04)
			}
		}

		switch req.Type {
		case "BootMouse":
			if s.relWriter != nil {
				var dx, dy, dw int8
				if ev.X != nil {
					dx = int8(*ev.X)
				}
				if ev.Y != nil {
					dy = int8(*ev.Y)
				}
				if ev.Wheel != nil {
					dw = int8(*ev.Wheel)
				}
				report := []byte{s.mouseBtnState, byte(dx), byte(dy), byte(dw)}

				select {
				case s.mouseChan <- report:
				default:
				}
				log.Printf("HID BootMouse Q: dX=%d, dY=%d", dx, dy)
			}
		case "AbsoluteMouse":
			if s.absWriter != nil {
				// Report ID: 1, Buttons, X (LE), Y (LE)
				report := make([]byte, 6)
				report[0] = 0x01
				report[1] = s.mouseBtnState
				if ev.X != nil {
					// Backend coord 0..1000 -> HID 0..32767
					val := uint16((*ev.X * 32767.0) / 1000.0)
					binary.LittleEndian.PutUint16(report[2:4], val)
				}
				if ev.Y != nil {
					val := uint16((*ev.Y * 32767.0) / 1000.0)
					binary.LittleEndian.PutUint16(report[4:6], val)
				}

				select {
				case s.absMouseChan <- report:
					// Queued successfully
				default:
					// Buffer full, drop event to keep server responsive
				}
				log.Printf("HID AbsMouse Queue: X=%v, Y=%v, Buttons=%02x", *ev.X, *ev.Y, s.mouseBtnState)
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleKeyboard(w http.ResponseWriter, r *http.Request) {
	var req KeyboardEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if s.kbWriter == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	s.keyStateMutex.Lock()
	defer s.keyStateMutex.Unlock()

	if req.Reset {
		s.keyState = s.keyState[:0]
	}

	for _, k := range req.Keys {
		code := uint16(k.ScanCode)
		if k.IsDown {
			found := false
			for _, existing := range s.keyState {
				if existing == code {
					found = true
					break
				}
			}
			if !found && len(s.keyState) < 6 {
				s.keyState = append(s.keyState, code)
			}
		} else {
			for i, existing := range s.keyState {
				if existing == code {
					s.keyState = append(s.keyState[:i], s.keyState[i+1:]...)
					break
				}
			}
		}
	}

	// Prepare 8-byte HID report: Modifiers, Reserved, Key0-Key5
	report := make([]byte, 8)
	var modifiers byte
	var keyIdx int
	for _, code := range s.keyState {
		// Modifier mapping (0xE0..0xE7)
		if code >= 224 && code <= 231 {
			modifiers |= 1 << (code - 224)
		} else if keyIdx < 6 {
			report[2+keyIdx] = byte(code)
			keyIdx++
		}
	}
	report[0] = modifiers

	select {
	case s.kbChan <- report:
	default:
	}

	log.Printf("HID Keyboard Q: Mod=%02x, Keys=%v", modifiers, s.keyState)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleMjpeg(w http.ResponseWriter, r *http.Request) {
	// Standard multipart MJPEG (fallback)
	m := multipart.NewWriter(w)
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+m.Boundary())

	lastCount := atomic.LoadUint64(&s.memory.UpdateCount)

	for {
		img := s.captureImage()

		part, err := m.CreatePart(textproto.MIMEHeader{
			"Content-Type": []string{"image/jpeg"},
		})
		if err != nil {
			return
		}

		if err := jpeg.Encode(part, img, &jpeg.Options{Quality: 80}); err != nil {
			return
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Wait for next update
		for {
			currentCount := atomic.LoadUint64(&s.memory.UpdateCount)
			if currentCount != lastCount {
				lastCount = currentCount
				break
			}
			time.Sleep(5 * time.Millisecond) // Yield CPU
		}
	}
}

func (s *Server) handleRects(w http.ResponseWriter, r *http.Request) {
	// Custom DisplayLink rects protocol (image/mjpeg+rects)
	w.Header().Set("Content-Type", "image/mjpeg+rects")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	lastCount := atomic.LoadUint64(&s.memory.UpdateCount)

	for {
		// Wait for next update with a larger debounce to group rapid drawing batches (wakeup, lock screen)
		for {
			currentCount := atomic.LoadUint64(&s.memory.UpdateCount)
			if currentCount != lastCount {
				// Settle time: wait for host to finish a logical refresh batch (Desktop refresh is heavy)
				time.Sleep(33 * time.Millisecond) // Approx 2-frame delay for stability
				lastCount = atomic.LoadUint64(&s.memory.UpdateCount)
				break
			}
			time.Sleep(5 * time.Millisecond) // Yield CPU
		}

		img := s.captureImage()

		// Encode to JPEG in memory first to get length
		buf := new(bytes.Buffer)
		if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: 80}); err != nil {
			return
		}

		jpegData := buf.Bytes()

		// Header (12 bytes, Little Endian)
		// uint16 screenWidth
		// uint16 screenHeight
		// uint16 rectX
		// uint16 rectY
		// uint32 length
		header := make([]byte, 12)
		binary.LittleEndian.PutUint16(header[0:2], uint16(img.Bounds().Dx()))
		binary.LittleEndian.PutUint16(header[2:4], uint16(img.Bounds().Dy()))
		binary.LittleEndian.PutUint16(header[4:6], 0) // rectX
		binary.LittleEndian.PutUint16(header[6:8], 0) // rectY
		binary.LittleEndian.PutUint32(header[8:12], uint32(len(jpegData)))

		if _, err := w.Write(header); err != nil {
			return
		}
		if _, err := w.Write(jpegData); err != nil {
			return
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Map ActiveOffset to likely buffer boundary (0x2c0000 is the identified stride)
		activeBuffer := (uint32(s.memory.ActiveOffset) / 0x2c0000) * 0x2c0000

		// Monitor stream health
		log.Printf("Sent MJPEG+Rect frame: %dx%d, RegOffset: 0x%x, ActiveOff: 0x%x, ActiveBuf: 0x%x, Size: %d",
			img.Bounds().Dx(), img.Bounds().Dy(), s.memory.FB16BaseOffset, s.memory.ActiveOffset, activeBuffer, len(jpegData))
	}
}

func (s *Server) captureImage() image.Image {
	s.memory.Mu.RLock()
	defer s.memory.Mu.RUnlock()

	width := s.memory.HorizontalResolution
	height := s.memory.VerticalResolution
	if width == 0 || height == 0 {
		width, height = 640, 480 // fallback
	}

	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))

	if s.memory.BlankOutput {
		// Return solid black frame if host requested blanking (monitor off / sleep)
		return img
	}

	offset := uint32(s.memory.FB16BaseOffset)
	stride := uint32(s.memory.FB16LineStride)
	if stride < width*2 {
		stride = width * 2
	}

	fb := s.memory.FrameBuffer
	physSize := uint32(displaylink.FBPhysSize)

	for y := uint32(0); y < height; y++ {
		yOffset := uint32(y * stride)
		for x := uint32(0); x < width; x++ {
			pixelOffset := (offset + yOffset + uint32(x*2)) & (physSize - 1)
			val := binary.LittleEndian.Uint16(fb[pixelOffset : pixelOffset+2])

			r := uint8((val >> 11) & 0x1F)
			g := uint8((val >> 5) & 0x3F)
			b := uint8(val & 0x1F)

			img.SetRGBA(int(x), int(y), color.RGBA{
				R: (r << 3) | (r >> 2),
				G: (g << 2) | (g >> 4),
				B: (b << 3) | (b >> 2),
				A: 255,
			})
		}
	}
	return img
}
