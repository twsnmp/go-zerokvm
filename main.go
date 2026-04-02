package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/twsnmp/go-zerokvm/pkg/displaylink"
	"github.com/twsnmp/go-zerokvm/pkg/server"
	"github.com/twsnmp/go-zerokvm/pkg/usb/configfs"
	"github.com/twsnmp/go-zerokvm/pkg/usb/functionfs"
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var assets embed.FS

func main() {
	udcName := flag.String("udc", "", "UDC name (e.g. fe980000.usb)")
	gadgetName := flag.String("name", "zerokvm", "Gadget name")
	listenAddr := flag.String("listen", ":8080", "Listen address for web UI")
	flag.Parse()

	if *udcName == "" {
		log.Fatal("UDC name is required (-udc)")
	}

	// 1. Initialize Memory
	mem := displaylink.NewMemory()

	// 2. Configure USB Gadget via ConfigFS
	gadget := configfs.NewGadget(*gadgetName)
	if err := setupGadget(gadget); err != nil {
		log.Fatalf("Failed to setup gadget: %v", err)
	}
	defer gadget.Delete()

	// 3. Initialize FunctionFS for DisplayLink
	if err := functionfs.Mount("dl", "/dev/dl"); err != nil {
		log.Printf("Warning: Failed to mount FunctionFS 'dl' to /dev/dl: %v", err)
	}

	dlEp0, err := functionfs.NewEp0("/dev/dl")
	if err != nil {
		log.Printf("Warning: Failed to open /dev/dl/ep0: %v. Are you running as root?", err)
	} else {
		defer dlEp0.Close()
		setupDisplayLinkFFS(dlEp0)

		// 3.5 BIND UDC (MUST be after FFS descriptors are written)
		log.Printf("Binding gadget to UDC %s", *udcName)
		if err := gadget.SetUDC(*udcName); err != nil {
			log.Fatalf("Failed to bind UDC: %v", err)
		}
	}

	// 4. Start Web Server
	subFS, err := fs.Sub(assets, "assets")
	if err != nil {
		log.Fatalf("Failed to create sub FS: %v", err)
	}
	srv := server.NewServer(mem, http.FS(subFS))
	go func() {
		log.Printf("Starting web server on %s", *listenAddr)
		if err := srv.Start(*listenAddr); err != nil {
			log.Fatalf("Web server failed: %v", err)
		}
	}()

	// 5. Main EP0 Event Loop and Receiver Lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if dlEp0 == nil {
			return
		}

		var ep1Cancel context.CancelFunc

		log.Println("Ready to handle EP0 events.")
		for {
			ev, err := dlEp0.ReadEvent()
			if err != nil {
				log.Printf("EP0 Read error: %v. (Waiting for host to reconnect...)", err)
				time.Sleep(2 * time.Second)
				continue
			}

			switch ev.Type {
			case functionfs.EventEnable:
				log.Println("USB Event: ENABLE (Host configured the device)")
				if ep1Cancel != nil {
					ep1Cancel()
				}
				var subCtx context.Context
				subCtx, ep1Cancel = context.WithCancel(ctx)
				go runReceiverLoop(subCtx, mem)

			case functionfs.EventDisable:
				log.Println("USB Event: DISABLE (Connection reset by host)")
				if ep1Cancel != nil {
					ep1Cancel()
					ep1Cancel = nil
				}

			case functionfs.EventSetup:
				displaylink.HandleVendorRequest(dlEp0, ev.Setup, mem)

			case functionfs.EventSuspend:
				log.Println("USB Event: SUSPEND")
			case functionfs.EventResume:
				log.Println("USB Event: RESUME")
			}
		}
	}()

	// Handle signals for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan
	log.Printf("Received signal %v, shutting down...", sig)

	// Cancellation of context will stop receiver loops if they check ctx.Done()
	cancel()

	// Force close endpoints to break any blocking Read() calls
	if dlEp0 != nil {
		log.Println("Closing EP0...")
		dlEp0.Close()
	}

	log.Println("Unbinding UDC...")
	gadget.SetUDC("") // Unbind UDC
	log.Println("Clean shutdown complete.")
}

func runReceiverLoop(ctx context.Context, mem *displaylink.Memory) {
	ep1, err := functionfs.OpenEndpoint("/dev/dl", 1, true)
	if err != nil {
		log.Printf("Error opening ep1 (OUT): %v", err)
		return
	}
	defer ep1.Close()

	decoder := displaylink.NewDecoder(mem)
	buf := make([]byte, 32768)

	var bulkReadCount uint64
	log.Println("DisplayLink receiver loop started.")
	for {
		select {
		case <-ctx.Done():
			log.Println("Receiver loop stopped by context.")
			return
		default:
			if atomic.LoadUint64(&bulkReadCount)%1000 == 0 {
				log.Printf("Waiting for EP1 OUT data... (Total Packets: %d)", bulkReadCount)
			}
			n, err := ep1.Read(buf)
			if err != nil {
				log.Printf("Read error on ep1: %v", err)
				return
			}
			if n > 0 && atomic.LoadUint64(&bulkReadCount)%100 == 0 {
				log.Printf("Bulk Read EP1: %d bytes (Packet #%d)", n, bulkReadCount)
			}

			if n > 0 {
				if atomic.AddUint64(&bulkReadCount, 1)%100 == 0 {
					log.Printf("Bulk Read EP1: %d bytes (Total reads: %d)", n, bulkReadCount)
				}
				if err := decoder.Decode(buf[:n]); err != nil {
					log.Printf("Decode error: %v", err)
				}
			}
		}
	}
}

func setupGadget(g *configfs.Gadget) error {
	if g.Exists() {
		_ = g.SetUDC("") // Unbind if already exists
		_ = g.Delete()
	}
	if err := g.Create(); err != nil {
		return err
	}

	g.SetVendor(0x17e9)  // DisplayLink
	g.SetProduct(0x4010) // ZeroKVM
	g.SetUSB(0x0200)

	// Composite Device Class (IAD)
	g.SetClass(0xef)
	g.SetSubClass(0x02)
	g.SetProtocol(0x01)

	s := g.Strings(0x409)
	if err := s.Create(); err != nil {
		return err
	}
	_ = s.SetManufacturer("ZeroKVM-Go")
	_ = s.SetProduct("ZeroKVM-Go")
	_ = s.SetSerialNumber("123456")

	c := g.Config("c.1")
	if err := c.Create(); err != nil {
		return err
	}
	_ = c.SetMaxPower(500)

	// --- Functions ---

	// 0. DisplayLink function (ffs.dl) - MUST BE FIRST for Interface 0
	if _, err := g.CreateFunction("ffs.dl"); err != nil {
		return fmt.Errorf("failed to create ffs.dl: %w", err)
	}
	if err := c.AddFunction("ffs.dl"); err != nil {
		return fmt.Errorf("failed to add ffs.dl to config: %w", err)
	}

	// 1. Keyboard (hid.usb0) - Interface 1
	kb, err := g.CreateFunction("hid.usb0")
	if err != nil {
		return fmt.Errorf("failed to create hid.usb0: %w", err)
	}
	_ = kb.SetProperty("protocol", "1")
	_ = kb.SetProperty("subclass", "1")
	_ = kb.SetProperty("report_length", "8")
	if err := kb.SetBinaryProperty("report_desc", displaylink.KeyboardDescriptor); err != nil {
		return fmt.Errorf("failed to write keyboard report desc: %w", err)
	}
	if err := c.AddFunction("hid.usb0"); err != nil {
		return fmt.Errorf("failed to add hid.usb0 to config: %w", err)
	}

	// 2. Absolute Mouse (hid.usb1) - Interface 2
	cms, err := g.CreateFunction("hid.usb1")
	if err != nil {
		return fmt.Errorf("failed to create hid.usb1: %w", err)
	}
	_ = cms.SetProperty("protocol", "0")
	_ = cms.SetProperty("subclass", "0")
	_ = cms.SetProperty("report_length", "6") // With Report ID
	if err := cms.SetBinaryProperty("report_desc", displaylink.AbsoluteMouseDescriptor); err != nil {
		return fmt.Errorf("failed to write abs mouse report desc: %w", err)
	}
	if err := c.AddFunction("hid.usb1"); err != nil {
		return fmt.Errorf("failed to add hid.usb1 to config: %w", err)
	}

	// 3. Relative Mouse (hid.usb2) - Interface 3
	rms, err := g.CreateFunction("hid.usb2")
	if err != nil {
		return fmt.Errorf("failed to create hid.usb2: %w", err)
	}
	_ = rms.SetProperty("protocol", "1")
	_ = rms.SetProperty("subclass", "1")
	_ = rms.SetProperty("report_length", "4") // Buttons, dX, dY, Wheel
	if err := rms.SetBinaryProperty("report_desc", displaylink.BootMouseDescriptor); err != nil {
		return fmt.Errorf("failed to write rel mouse report desc: %w", err)
	}
	if err := c.AddFunction("hid.usb2"); err != nil {
		return fmt.Errorf("failed to add hid.usb2 to config: %w", err)
	}

	return nil
}

func setupDisplayLinkFFS(ep0 *functionfs.Ep0) {
	// DisplayLink is Interface 0
	const dlInterfaceNum = 0

	// Full Speed descriptors
	fs := [][]byte{
		{0x09, 0x04, dlInterfaceNum, 0x00, 0x01, 0xff, 0x00, 0x00, 0x00}, // 1 EP, Proto 0
		{0x07, 0x05, 0x01, 0x02, 0x40, 0x00, 0x00},                       // EP1 OUT Bulk (64)
	}

	// High Speed descriptors
	hs := [][]byte{
		{0x09, 0x04, dlInterfaceNum, 0x00, 0x01, 0xff, 0x00, 0x00, 0x00}, // 1 EP, Proto 0
		{0x07, 0x05, 0x01, 0x02, 0x00, 0x02, 0x00},                       // EP1 OUT Bulk (512)
	}

	log.Println("Writing standard descriptors to EP0...")
	if err := ep0.WriteDescriptors(fs, hs, nil); err != nil {
		log.Printf("Error: Critical failure writing standard descriptors: %v", err)
		return
	}

	if err := ep0.WriteStrings(0x409, []string{"ZeroKVM", "ZeroKVM", "123456"}); err != nil {
		log.Printf("Failed to write strings: %v", err)
	}
}
