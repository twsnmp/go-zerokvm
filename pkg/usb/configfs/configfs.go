package configfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DefaultGadgetsBasePath = "/sys/kernel/config/usb_gadget"

type Gadget struct {
	Path string
}

func NewGadget(name string) *Gadget {
	return &Gadget{
		Path: filepath.Join(DefaultGadgetsBasePath, name),
	}
}

func (g *Gadget) Exists() bool {
	_, err := os.Stat(g.Path)
	return err == nil
}

func (g *Gadget) Create() error {
	return os.MkdirAll(g.Path, 0755)
}

func (g *Gadget) Delete() error {
	if !g.Exists() {
		return nil
	}

	// 1. Unbind UDC
	_ = g.SetUDC("")

	// 2. Remove functions from configs
	configsPath := filepath.Join(g.Path, "configs")
	configs, _ := os.ReadDir(configsPath)
	for _, c := range configs {
		cPath := filepath.Join(configsPath, c.Name())
		// Remove symlinks (functions)
		items, _ := os.ReadDir(cPath)
		for _, item := range items {
			if item.Type()&os.ModeSymlink != 0 {
				_ = os.Remove(filepath.Join(cPath, item.Name()))
			}
		}
		// Remove strings in configs
		_ = os.RemoveAll(filepath.Join(cPath, "strings"))
		// Remove config dir
		_ = os.Remove(cPath)
	}

	// 3. Remove functions
	_ = os.RemoveAll(filepath.Join(g.Path, "functions"))

	// 4. Remove strings
	_ = os.RemoveAll(filepath.Join(g.Path, "strings"))

	// 5. Finally remove gadget root
	return os.Remove(g.Path)
}

func (g *Gadget) writeAttr(name string, value string) error {
	return os.WriteFile(filepath.Join(g.Path, name), []byte(value+"\n"), 0644)
}

func (g *Gadget) readAttr(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(g.Path, name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (g *Gadget) writeUintHex(name string, value uint64, bits int) error {
	format := "0x%0" + strconv.Itoa(bits/4) + "x"
	return g.writeAttr(name, fmt.Sprintf(format, value))
}

func (g *Gadget) SetVendor(id uint16) error   { return g.writeUintHex("idVendor", uint64(id), 16) }
func (g *Gadget) SetProduct(id uint16) error  { return g.writeUintHex("idProduct", uint64(id), 16) }
func (g *Gadget) SetDevice(bcd uint16) error  { return g.writeUintHex("bcdDevice", uint64(bcd), 16) }
func (g *Gadget) SetUSB(bcd uint16) error     { return g.writeUintHex("bcdUSB", uint64(bcd), 16) }
func (g *Gadget) SetClass(cls byte) error     { return g.writeUintHex("bDeviceClass", uint64(cls), 8) }
func (g *Gadget) SetSubClass(sub byte) error  { return g.writeUintHex("bDeviceSubClass", uint64(sub), 8) }
func (g *Gadget) SetProtocol(proto byte) error { return g.writeUintHex("bDeviceProtocol", uint64(proto), 8) }

func (g *Gadget) SetUDC(name string) error {
	return g.writeAttr("UDC", name)
}

func (g *Gadget) GetUDC() (string, error) {
	return g.readAttr("UDC")
}

type Strings struct {
	Path string
}

func (g *Gadget) Strings(lang uint16) *Strings {
	return &Strings{
		Path: filepath.Join(g.Path, "strings", fmt.Sprintf("0x%x", lang)),
	}
}

func (s *Strings) Create() error {
	return os.MkdirAll(s.Path, 0755)
}

func (s *Strings) writeAttr(name string, value string) error {
	return os.WriteFile(filepath.Join(s.Path, name), []byte(value+"\n"), 0644)
}

func (s *Strings) SetManufacturer(m string) error { return s.writeAttr("manufacturer", m) }
func (s *Strings) SetProduct(p string) error      { return s.writeAttr("product", p) }
func (s *Strings) SetSerialNumber(sn string) error { return s.writeAttr("serialnumber", sn) }

type Config struct {
	GadgetPath string
	Name       string
	Path       string
}

func (g *Gadget) Config(name string) *Config {
	return &Config{
		GadgetPath: g.Path,
		Name:       name,
		Path:       filepath.Join(g.Path, "configs", name),
	}
}

func (c *Config) Create() error {
	return os.MkdirAll(c.Path, 0755)
}

func (c *Config) Strings(lang uint16) *Strings {
	return &Strings{
		Path: filepath.Join(c.Path, "strings", fmt.Sprintf("0x%x", lang)),
	}
}

func (c *Config) SetMaxPower(mw int) error {
	return os.WriteFile(filepath.Join(c.Path, "MaxPower"), []byte(fmt.Sprintf("%d\n", mw)), 0644)
}

func (c *Config) AddFunction(name string) error {
	target := filepath.Join(c.GadgetPath, "functions", name)
	link := filepath.Join(c.Path, name)
	return os.Symlink(target, link)
}

func (c *Config) RemoveFunction(name string) error {
	return os.Remove(filepath.Join(c.Path, name))
}

type Function struct {
	Path string
}

func (g *Gadget) CreateFunction(name string) (*Function, error) {
	path := filepath.Join(g.Path, "functions", name)
	if err := os.MkdirAll(path, 0755); err != nil {
		return nil, err
	}
	return &Function{Path: path}, nil
}

func (f *Function) SetProperty(name string, value string) error {
	return os.WriteFile(filepath.Join(f.Path, name), []byte(value+"\n"), 0644)
}

func (f *Function) SetBinaryProperty(name string, value []byte) error {
	return os.WriteFile(filepath.Join(f.Path, name), value, 0644)
}
