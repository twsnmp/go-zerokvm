# go-zerokvm

[日本語の説明](README_ja.md)

This project is a Go port of the original [ZeroKVM](https://github.com/doominator42/ZeroKVM) by [doominator42](https://github.com/doominator42).
Go-zerokvm is a low-cost, easy-to-build KVM-over-IP device. It presents itself to the target host as a DisplayLink monitor and a standard USB keyboard/mouse.
We would like to express our gratitude to doominator42 for their incredible work on the original project.

![go-zerokvm](images/go-zerokvm.png)

## Features

- **DisplayLink Protocol Implementation**: Captures screen signals from the host and displays them in a web browser.
- **USB HID Emulation**: Emulates keyboard, absolute mouse, and relative mouse.
- **Web Console**: Intuitive remote operation directly from your browser.
- **Multi-Architecture Support**: Works on ARM-based Linux devices such as Raspberry Pi Zero/2/3/4/5.
- **No CGO Required**: Pure Go implementation makes cross-compilation extremely easy.

## Screen Shots

![go-zerokvm](images/screenshot.png)

## Requirements

- **Hardware**: Linux device with USB OTG (USB 2.0 Device/Gadget mode) support (e.g., Raspberry Pi Zero, 4, 5).
- **OS**: Linux with ConfigFS and FunctionFS enabled.

## Setup (ZeroKVM Device Side)

For Raspberry Pi users, the following configuration is required:

### 1. Enable Kernel Overlays
Add the following line to `/boot/config.txt` (or `/boot/firmware/config.txt`):
```text
dtoverlay=dwc2
```

### 2. Load Kernel Modules
Add these lines to `/etc/modules` and reboot, or load them manually via `modprobe`:
```text
dwc2
libcomposite
```

### 3. Permissions
Since this program configures USB gadgets (ConfigFS) and manipulates FunctionFS endpoints, it must be run with **root privileges** (sudo).

## Host PC (Target) Requirements

The target PC connected to ZeroKVM requires DisplayLink drivers.

- **Linux**: Mainline `udl` driver is included in kernels 3.4+. Check with `lsmod | grep udl` if not recognized.
- **Windows**: Drivers are typically installed automatically via Windows Update.
- **macOS / Android**: "DisplayLink Manager" (or DisplayLink Presenter) must be installed from the official website.

## Configuration

You can specify the following arguments at runtime:

| Argument | Description | Default |
| :--- | :--- | :--- |
| `-udc` | Name of the USB controller (Required) | (none) |
| `-name` | Gadget name | `zerokvm` |
| `-listen` | Listen address for the Web UI | `:8080` |

### Identifying the UDC Name
```bash
ls /sys/class/udc
# Example: fe980000.usb
```

## Operation

1. **Build**:
   Using [mise](https://mise.jdx.dev/):
   ```bash
   mise run build:all
   ```
   Manual build (e.g., for ARM64):
   ```bash
   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o go-zerokvm
   ```

2. **Run**:
   ```bash
   sudo ./go-zerokvm -udc <your-udc-name>
   ```

3. **Usage**:
   Connect the ZeroKVM device's USB OTG port to the host PC's USB port, and access `http://<device-ip>:8080` in your web browser.

## License
This project is licensed under the [Apache License 2.0](LICENSE).
