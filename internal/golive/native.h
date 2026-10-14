// The parts of a screen share that run as C: capturing the screen or a
// window with GDI, which Windows 7 has, scaling and converting what is
// captured, and encoding it to H264 with Cisco's OpenH264, loaded at run
// time.

#ifndef LINKCORD_GOLIVE_NATIVE_H
#define LINKCORD_GOLIVE_NATIVE_H

#include <stdint.h>

// lc_h264_load loads OpenH264 from path, once. It returns 0, or the error
// of Windows, or -1 when the library lacks the encoder.
int lc_h264_load(const uint16_t *path);

typedef struct lc_encoder lc_encoder;

// lc_h264_open makes an encoder of w by h pictures, fps a second, at about
// bitrate bits a second, with an IDR picture every idr pictures. On
// failure, it returns NULL and sets *err.
lc_encoder *lc_h264_open(int w, int h, int fps, int bitrate, int idr, int *err);

// lc_h264_encode encodes the I420 picture i420, of the encoder's size. It
// points *out to the access unit, in Annex B, valid until the next call, or
// sets *out_len to 0 when the encoder skips the picture. *idr reports an IDR
// picture. It returns 0 or the encoder's error.
int lc_h264_encode(lc_encoder *e, const uint8_t *i420, int64_t ts_ms, uint8_t **out, int *out_len, int *idr);

// lc_h264_force_idr makes the next picture an IDR picture.
void lc_h264_force_idr(lc_encoder *e);

// lc_h264_set_bitrate changes the bitrate.
void lc_h264_set_bitrate(lc_encoder *e, int bitrate);

void lc_h264_close(lc_encoder *e);

// lc_bgra_to_i420 scales the BGRA picture src, sw by sh with rows sstride
// bytes apart, to dw by dh, averaging the pixels each covers, and writes it
// to dst in I420 (BT.601, limited range). dw and dh must be even.
void lc_bgra_to_i420(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh);

// lc_bgra_to_rgba is lc_bgra_to_i420 writing opaque RGBA instead.
void lc_bgra_to_rgba(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh);

typedef struct lc_capture lc_capture;

// lc_capture_open captures the window hwnd, or the area of the screen at
// x, y of w by h pixels if hwnd is 0, with the mouse pointer if cursor.
lc_capture *lc_capture_open(uintptr_t hwnd, int x, int y, int w, int h, int cursor);

// lc_capture_grab captures a picture and returns it in BGRA, top row first,
// valid until the next call, or NULL when there is nothing to capture, as
// for a window minimized or closed. *gone reports a window closed.
const uint8_t *lc_capture_grab(lc_capture *c, int *w, int *h, int *stride, int *gone);

void lc_capture_close(lc_capture *c);

// lc_source is a screen or a window that can be shared.
typedef struct {
	uintptr_t hwnd;
	int x, y, w, h;
	int primary;
	uint16_t title[256];
} lc_source;

// lc_list_monitors and lc_list_windows fill out with up to max screens or
// windows worth sharing and return how many. lc_list_windows leaves out
// the window self.
int lc_list_monitors(lc_source *out, int max);
int lc_list_windows(lc_source *out, int max, uintptr_t self);

// lc_ploop is the sound of some processes, captured as Windows 10 2004 and
// later allow: that of the process tree of pid if include, or else of all
// but it.
typedef struct lc_ploop lc_ploop;

// lc_ploop_open starts capturing. It returns NULL and sets *err, -1 where
// Windows cannot. It must be used from one thread only.
lc_ploop *lc_ploop_open(uint32_t pid, int include, int *err);

// lc_ploop_read copies up to max frames of 48 kHz stereo 16-bit PCM
// captured into dst and returns how many, or -1 once the capture failed.
int lc_ploop_read(lc_ploop *c, int16_t *dst, int max);

void lc_ploop_close(lc_ploop *c);

#endif
