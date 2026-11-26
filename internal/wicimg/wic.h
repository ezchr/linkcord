#ifndef LINKCORD_WIC_H
#define LINKCORD_WIC_H

#include <stddef.h>
#include <stdint.h>

// Limits for untrusted pictures: the largest side and pixel count read, a
// GIF canvas kept whole while its frames are drawn, and a picture made.
#define WIC_MAX_IN_SIDE 30000
#define WIC_MAX_IN_PIXELS (64LL << 20)
#define WIC_MAX_CANVAS_PIXELS (12LL << 20)
#define WIC_MAX_OUT_SIDE 8192
#define WIC_MAX_OUT_PIXELS (16LL << 20)

typedef struct {
	int w, h;               // the still's frame
	int frames;
	int jpeg, gif;
	int canvas_w, canvas_h; // a GIF's logical screen
} wic_info_t;

int wic_info(const void *data, size_t len, wic_info_t *info);
int wic_still(const void *data, size_t len, int tw, int th, uint8_t *dst);
int wic_gif(const void *data, size_t len, int cw, int ch, int tw, int th, int step, int keep, uint8_t *dst, int *delays, int ndelays);

#endif
