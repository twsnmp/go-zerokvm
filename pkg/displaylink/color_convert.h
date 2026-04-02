#ifndef COLOR_CONVERT_H
#define COLOR_CONVERT_H

#include <stdint.h>
#include <stddef.h>

#ifdef __aarch64__
#include <arm_neon.h>
#endif

// Convert RGB565 Big-Endian to RGB565 Little-Endian
uint16_t copy_rgb565_be_to_le(const uint16_t *source, uint16_t *destination, size_t count);

// Convert RGB565 Little-Endian to RGBx (32-bit)
void copy_rgb565_le_to_rgbx(const uint16_t *source, uint32_t *destination, size_t count);

#endif // COLOR_CONVERT_H
