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

// lc_bgra_to_yuv is lc_bgra_to_i420 for the destination rows y0 to y1 only
// (y0 even), so that threads can each take a band of one picture, and
// writing NV12 rather than I420 if nv12.
void lc_bgra_to_yuv(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh, int nv12, int y0, int y1);

// lc_simd, 1 by default, has conversions use SSE2; tests clear it.
extern int lc_simd;

typedef struct lc_hwenc lc_hwenc;

// lc_hwenc_open opens the graphics card's H264 encoder through Media
// Foundation, for w by h NV12 pictures, fps a second, at bitrate, with an
// IDR picture every idr pictures, and copies its name into name. It returns
// NULL where there is none. Every call on it must come from one thread.
lc_hwenc *lc_hwenc_open(int w, int h, int fps, int bitrate, int idr, uint16_t *name, int name_len);

// lc_hwenc_open_on is lc_hwenc_open with the encoder of the graphics card
// of device, an ID3D11Device, using its textures; NULL if that card has no
// encoder that takes them.
lc_hwenc *lc_hwenc_open_on(void *device, int w, int h, int fps, int bitrate, int idr, uint16_t *name, int name_len);

// lc_hwenc_encode_texture is lc_hwenc_encode for an NV12 ID3D11Texture2D of
// the encoder's device, rather than a picture in memory.
int lc_hwenc_encode_texture(lc_hwenc *e, void *texture, int64_t ts_ms, int force_idr, uint8_t **out, int *out_len, int64_t *out_ts_ms, int *idr);

// lc_hwenc_encode gives the encoder the NV12 picture nv12 and returns the
// oldest picture it encoded, if any, in *out (Annex B, valid until the next
// call), with its time and whether it is an IDR picture. A hardware encoder
// may hand a picture back one call later. It returns 0, or -1 on failure.
int lc_hwenc_encode(lc_hwenc *e, const uint8_t *nv12, int64_t ts_ms, int force_idr, uint8_t **out, int *out_len, int64_t *out_ts_ms, int *idr);

void lc_hwenc_set_bitrate(lc_hwenc *e, int bitrate);
void lc_hwenc_close(lc_hwenc *e);

// lc_bgra_to_rgba is lc_bgra_to_i420 writing opaque RGBA instead.
void lc_bgra_to_rgba(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh);

typedef struct lc_capture lc_capture;

// lc_capture_open captures the window hwnd, or the area of the screen at
// x, y of w by h pixels if hwnd is 0, with the mouse pointer if cursor.
lc_capture *lc_capture_open(uintptr_t hwnd, int x, int y, int w, int h, int cursor);

// lc_capture_grab captures a picture and returns it in BGRA, top row first,
// valid until the next call, or NULL when there is nothing to capture, as
// for a window minimized or closed. *gone reports a window closed, and
// *changed whether the picture may differ from the last one returned: 0
// only when the screen and the pointer are known not to have changed.
const uint8_t *lc_capture_grab(lc_capture *c, int *w, int *h, int *stride, int *gone, int *changed);

// lc_capture_duplicates reports whether the screen is captured with DXGI's
// desktop duplication rather than GDI.
int lc_capture_duplicates(lc_capture *c);

// lc_capture_release ends the capture's duplication of the screen, which
// the next grab begins again. A program can duplicate a screen only once:
// the graphics card's capture cannot while this one holds it.
void lc_capture_release(lc_capture *c);

void lc_capture_close(lc_capture *c);

// lc_dpi_aware has the calling thread see screens in their own pixels and
// returns what lc_dpi_restore takes to undo it.
void *lc_dpi_aware(void);
void lc_dpi_restore(void *old);

typedef struct lc_dupl lc_dupl;

// lc_dupl_open duplicates the screen whose top left is at x, y on the
// desktop, as Windows 8 and later can, which sees games in full screen.
lc_dupl *lc_dupl_open(int x, int y);

// lc_dupl_grab points *pix to the latest picture of the screen, in BGRA
// with rows *w*4 bytes apart, valid until the next call, and returns 1. It
// returns 0 when there is no picture yet, and -1 when the screen cannot be
// duplicated, as on Windows 7.
int lc_dupl_grab(lc_dupl *d, const uint8_t **pix, int *w, int *h);

// lc_dupl_seq counts the pictures lc_dupl_grab has had: the same number
// means the same picture.
int lc_dupl_seq(lc_dupl *d);

void lc_dupl_close(lc_dupl *d);

// lc_gpucap captures a screen, or a window that shows uncovered on one, and
// converts it to NV12 on the graphics card, as Windows 8 and later can: the
// picture never reaches the processor. lc_gpucap_open returns NULL where
// Direct3D 11 is missing.
typedef struct lc_gpucap lc_gpucap;
lc_gpucap *lc_gpucap_open(uintptr_t hwnd, int x, int y, int cursor);

// lc_gpucap_grab takes the latest picture, w by h. It returns 1 with one
// (*changed 0 when it is the same as the last), 0 with none for now, -2
// when this picture must be captured by the processor instead (a window
// covered or across screens), and -1 when the graphics card cannot capture
// at all, as on Windows 7. *gone reports a window closed.
int lc_gpucap_grab(lc_gpucap *g, int *w, int *h, int *changed, int *gone);

// lc_gpucap_convert scales the last picture grabbed to an ow by oh NV12
// texture and sets *slot to it: the textures are used in turn, eight of
// them. It returns 0, or -1 when the graphics card cannot convert.
int lc_gpucap_convert(lc_gpucap *g, int ow, int oh, int *slot);

// lc_gpucap_device is the ID3D11Device of the textures, which changes when
// lc_gpucap_gen does; lc_gpucap_texture the ID3D11Texture2D of a slot. Both
// come with a reference of their own, which lc_com_release lets go of.
void *lc_gpucap_device(lc_gpucap *g);
int lc_gpucap_gen(lc_gpucap *g);
void *lc_gpucap_texture(lc_gpucap *g, int slot);

// lc_gpucap_preview scales the last picture to w by h RGBA into rgba.
int lc_gpucap_preview(lc_gpucap *g, uint8_t *rgba, int w, int h);
void lc_gpucap_close(lc_gpucap *g);
// lc_texture_read_nv12 reads a w by h NV12 texture back into dst, for tests.
int lc_texture_read_nv12(void *texture, uint8_t *dst, int w, int h);

void lc_com_addref(void *p);
void lc_com_release(void *p);

// lc_window_uncovered reports whether the window shows uncovered on the
// screen over its client area at sx, sy, w by h.
int lc_window_uncovered(uintptr_t hwnd, int sx, int sy, int w, int h);

// lc_mt_protect has a Direct3D 11 device take calls from several threads.
void lc_mt_protect(void *device);

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

// lc_audio_pids fills out with up to max processes playing sound now, on any
// speakers, and returns how many.
int lc_audio_pids(uint32_t *out, int max);

#endif
