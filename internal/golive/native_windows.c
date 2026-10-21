//go:build windows && !no_voice

#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdlib.h>
#include <string.h>
#include <emmintrin.h>

#include "codec_api.h"
#include "native.h"

// ---- OpenH264 --------------------------------------------------------------

typedef int (*create_encoder_fn)(ISVCEncoder **);
typedef void (*destroy_encoder_fn)(ISVCEncoder *);

static HMODULE openh264;
static create_encoder_fn create_encoder;
static destroy_encoder_fn destroy_encoder;

int lc_h264_load(const uint16_t *path) {
	if (openh264) {
		return 0;
	}
	HMODULE m = LoadLibraryW((const wchar_t *)path);
	if (!m) {
		return (int)GetLastError();
	}
	create_encoder = (create_encoder_fn)GetProcAddress(m, "WelsCreateSVCEncoder");
	destroy_encoder = (destroy_encoder_fn)GetProcAddress(m, "WelsDestroySVCEncoder");
	if (!create_encoder || !destroy_encoder) {
		FreeLibrary(m);
		return -1;
	}
	openh264 = m;
	return 0;
}

struct lc_encoder {
	ISVCEncoder *enc;
	int w, h;
	uint8_t *buf;
	int cap;
	SFrameBSInfo info;
};

lc_encoder *lc_h264_open(int w, int h, int fps, int bitrate, int idr, int *err) {
	ISVCEncoder *e = NULL;
	if (!create_encoder) {
		*err = -1;
		return NULL;
	}
	int r = create_encoder(&e);
	if (r || !e) {
		*err = r ? r : -1;
		return NULL;
	}
	SEncParamExt p;
	memset(&p, 0, sizeof p);
	(*e)->GetDefaultParams(e, &p);
	// Camera mode, not screen content: measured on a picture moving as in a
	// game, it encodes over three times faster (2.4 ms against 8.1 at 720p)
	// and uses the bitrate it is given; screen content's slower encoding
	// cannot keep up with 60 pictures a second at 1080p.
	p.iUsageType = CAMERA_VIDEO_REAL_TIME;
	p.iPicWidth = w;
	p.iPicHeight = h;
	p.iTargetBitrate = bitrate;
	p.iMaxBitrate = bitrate * 3 / 2;
	p.iRCMode = RC_BITRATE_MODE;
	p.fMaxFrameRate = (float)fps;
	p.iTemporalLayerNum = 1;
	p.iSpatialLayerNum = 1;
	p.iComplexityMode = LOW_COMPLEXITY;
	p.uiIntraPeriod = (unsigned int)idr;
	p.iNumRefFrame = 1;
	p.eSpsPpsIdStrategy = CONSTANT_ID;
	p.bPrefixNalAddingCtrl = false;
	p.bEnableSSEI = false;
	p.iEntropyCodingModeFlag = 0;
	// Never skip pictures to keep the bitrate, which lowers the pictures a
	// second: quality gives way instead, as Discord's own streams do.
	p.bEnableFrameSkip = false;
	p.iMultipleThreadIdc = 0;
	p.bEnableDenoise = false;
	p.bEnableBackgroundDetection = true;
	p.bEnableAdaptiveQuant = true;
	p.bEnableSceneChangeDetect = true;
	p.bEnableLongTermReference = false;
	p.iLoopFilterDisableIdc = 0;
	SSpatialLayerConfig *l = &p.sSpatialLayers[0];
	l->iVideoWidth = w;
	l->iVideoHeight = h;
	l->fFrameRate = (float)fps;
	l->iSpatialBitrate = bitrate;
	l->iMaxSpatialBitrate = p.iMaxBitrate;
	l->uiProfileIdc = PRO_BASELINE;
	// Slices a picture into as many parts as there are cores, which are
	// encoded at once: old PCs need it to keep up.
	l->sSliceArgument.uiSliceMode = SM_FIXEDSLCNUM_SLICE;
	l->sSliceArgument.uiSliceNum = 0;

	int trace = WELS_LOG_QUIET;
	(*e)->SetOption(e, ENCODER_OPTION_TRACE_LEVEL, &trace);
	r = (*e)->InitializeExt(e, &p);
	if (r) {
		destroy_encoder(e);
		*err = r;
		return NULL;
	}
	int format = videoFormatI420;
	(*e)->SetOption(e, ENCODER_OPTION_DATAFORMAT, &format);

	lc_encoder *c = calloc(1, sizeof *c);
	c->enc = e;
	c->w = w;
	c->h = h;
	return c;
}

int lc_h264_encode(lc_encoder *c, const uint8_t *i420, int64_t ts_ms, uint8_t **out, int *out_len, int *idr) {
	ISVCEncoder *e = c->enc;
	SSourcePicture pic;
	memset(&pic, 0, sizeof pic);
	pic.iColorFormat = videoFormatI420;
	pic.iPicWidth = c->w;
	pic.iPicHeight = c->h;
	pic.iStride[0] = c->w;
	pic.iStride[1] = pic.iStride[2] = c->w / 2;
	pic.pData[0] = (unsigned char *)i420;
	pic.pData[1] = pic.pData[0] + c->w * c->h;
	pic.pData[2] = pic.pData[1] + (c->w / 2) * (c->h / 2);
	pic.uiTimeStamp = ts_ms;

	memset(&c->info, 0, sizeof c->info);
	int r = (*e)->EncodeFrame(e, &pic, &c->info);
	*out = NULL;
	*out_len = 0;
	*idr = 0;
	if (r) {
		return r;
	}
	if (c->info.eFrameType == videoFrameTypeSkip || c->info.eFrameType == videoFrameTypeInvalid) {
		return 0;
	}
	int total = 0;
	for (int i = 0; i < c->info.iLayerNum; i++) {
		SLayerBSInfo *l = &c->info.sLayerInfo[i];
		for (int n = 0; n < l->iNalCount; n++) {
			total += l->pNalLengthInByte[n];
		}
	}
	if (total > c->cap) {
		free(c->buf);
		c->cap = total * 2;
		c->buf = malloc(c->cap);
		if (!c->buf) {
			c->cap = 0;
			return -1;
		}
	}
	int at = 0;
	for (int i = 0; i < c->info.iLayerNum; i++) {
		SLayerBSInfo *l = &c->info.sLayerInfo[i];
		int size = 0;
		for (int n = 0; n < l->iNalCount; n++) {
			size += l->pNalLengthInByte[n];
		}
		memcpy(c->buf + at, l->pBsBuf, size);
		at += size;
	}
	*out = c->buf;
	*out_len = at;
	*idr = c->info.eFrameType == videoFrameTypeIDR;
	return 0;
}

void lc_h264_force_idr(lc_encoder *c) {
	(*c->enc)->ForceIntraFrame(c->enc, true);
}

void lc_h264_set_bitrate(lc_encoder *c, int bitrate) {
	SBitrateInfo b;
	memset(&b, 0, sizeof b);
	b.iLayer = SPATIAL_LAYER_ALL;
	b.iBitrate = bitrate;
	(*c->enc)->SetOption(c->enc, ENCODER_OPTION_BITRATE, &b);
	b.iBitrate = bitrate * 3 / 2;
	(*c->enc)->SetOption(c->enc, ENCODER_OPTION_MAX_BITRATE, &b);
}

void lc_h264_close(lc_encoder *c) {
	if (!c) {
		return;
	}
	(*c->enc)->Uninitialize(c->enc);
	destroy_encoder(c->enc);
	free(c->buf);
	free(c);
}

// ---- Scaling ---------------------------------------------------------------

// lc_inv[n] is 2^32 / n rounded up: a multiplication and a shift by it
// divide a sum of up to 64 bytes by n exactly, many times faster than a
// division, which scaling does three times a pixel.
static uint32_t lc_inv[65];

// Filled as the program starts, before any thread converts.
__attribute__((constructor)) static void init_inv(void) {
	for (int n = 2; n <= 64; n++) {
		lc_inv[n] = (uint32_t)((((uint64_t)1 << 32) + n - 1) / n);
	}
}

static inline unsigned div_n(unsigned s, unsigned n) {
	if (n == 1) {
		return s;
	}
	return n <= 64 ? (unsigned)(((uint64_t)s * lc_inv[n]) >> 32) : s / n;
}

// box_average averages the BGRA pixels of src that the destination pixel
// x, y of a dw by dh picture covers.
static inline void box_average(const uint8_t *src, int sstride, const int *xs, const int *ys, int x, int y, int *r, int *g, int *b) {
	int x0 = xs[x], x1 = xs[x + 1], y0 = ys[y], y1 = ys[y + 1];
	unsigned sr = 0, sg = 0, sb = 0, n = 0;
	for (int sy = y0; sy < y1; sy++) {
		const uint8_t *p = src + (size_t)sy * sstride + (size_t)x0 * 4;
		for (int sx = x0; sx < x1; sx++, p += 4) {
			sb += p[0];
			sg += p[1];
			sr += p[2];
		}
		n += x1 - x0;
	}
	*r = div_n(sr, n);
	*g = div_n(sg, n);
	*b = div_n(sb, n);
}

// bounds fills out, of n+1 entries, with where each of n destination
// pixels starts in s source pixels, each covering at least one.
static int *bounds(int s, int n) {
	int *out = malloc(sizeof(int) * (n + 1));
	for (int i = 0; i <= n; i++) {
		out[i] = (int)((int64_t)i * s / n);
	}
	for (int i = 0; i < n; i++) {
		if (out[i + 1] <= out[i]) {
			out[i + 1] = out[i] + 1;
		}
		if (out[i + 1] > s) {
			out[i + 1] = s;
			if (out[i] >= s) {
				out[i] = s - 1;
			}
		}
	}
	return out;
}

#define LC_Y(r, g, b) (uint8_t)(((66 * (r) + 129 * (g) + 25 * (b) + 128) >> 8) + 16)
#define LC_U(r, g, b) (uint8_t)(((-38 * (r) - 74 * (g) + 112 * (b) + 128) >> 8) + 128)
#define LC_V(r, g, b) (uint8_t)(((112 * (r) - 94 * (g) - 18 * (b) + 128) >> 8) + 128)

// put_uv writes the chroma of the 2x2 block at column x of chroma row cy.
static inline void put_uv(uint8_t *U, uint8_t *V, uint8_t *UV, int cw, int cy, int x, int r, int g, int b) {
	if (UV) {
		UV[(size_t)cy * cw * 2 + x * 2] = LC_U(r, g, b);
		UV[(size_t)cy * cw * 2 + x * 2 + 1] = LC_V(r, g, b);
	} else {
		U[(size_t)cy * cw + x] = LC_U(r, g, b);
		V[(size_t)cy * cw + x] = LC_V(r, g, b);
	}
}

// lc_simd is cleared by tests, to check the SSE2 code against plain C.
int lc_simd = 1;

// luma4 is the Y of the 4 BGRA pixels of px, in 32-bit lanes: SSE2, which
// every 64-bit processor has, Windows 7's oldest included.
static inline __m128i luma4(__m128i px) {
	const __m128i zero = _mm_setzero_si128();
	const __m128i k = _mm_set_epi16(0, 66, 129, 25, 0, 66, 129, 25);
	__m128i lo = _mm_madd_epi16(_mm_unpacklo_epi8(px, zero), k); // 25B+129G, 66R of pixels 0, 1
	__m128i hi = _mm_madd_epi16(_mm_unpackhi_epi8(px, zero), k); // of pixels 2, 3
	lo = _mm_add_epi32(lo, _mm_srli_epi64(lo, 32));
	hi = _mm_add_epi32(hi, _mm_srli_epi64(hi, 32));
	__m128i s = _mm_unpacklo_epi64(_mm_shuffle_epi32(lo, _MM_SHUFFLE(3, 3, 2, 0)), _mm_shuffle_epi32(hi, _MM_SHUFFLE(3, 3, 2, 0)));
	return _mm_add_epi32(_mm_srai_epi32(_mm_add_epi32(s, _mm_set1_epi32(128)), 8), _mm_set1_epi32(16));
}

// sum2x2 adds the 16-bit B, G, R, A of the 2x2 blocks of pixels a0/a1 over
// c0/c1 (2 pixels each): blocks 0 and 1, in the low and high halves.
static inline __m128i sum2x2(__m128i a, __m128i c) {
	const __m128i zero = _mm_setzero_si128();
	__m128i lo = _mm_add_epi16(_mm_unpacklo_epi8(a, zero), _mm_unpacklo_epi8(c, zero)); // pixels 0, 1
	__m128i hi = _mm_add_epi16(_mm_unpackhi_epi8(a, zero), _mm_unpackhi_epi8(c, zero)); // pixels 2, 3
	lo = _mm_add_epi16(lo, _mm_srli_si128(lo, 8));
	hi = _mm_add_epi16(hi, _mm_srli_si128(hi, 8));
	return _mm_unpacklo_epi64(lo, hi);
}

// chroma2 is U or V (coefficients k) of the two averaged blocks in avg, in
// the 32-bit lanes 0 and 1.
static inline __m128i chroma2(__m128i avg, __m128i k) {
	__m128i m = _mm_madd_epi16(avg, k);
	m = _mm_add_epi32(m, _mm_srli_epi64(m, 32));
	m = _mm_shuffle_epi32(m, _MM_SHUFFLE(3, 3, 2, 0));
	return _mm_add_epi32(_mm_srai_epi32(_mm_add_epi32(m, _mm_set1_epi32(128)), 8), _mm_set1_epi32(128));
}

// same_size converts rows y0 to y1 of a picture that is not scaled: eight
// pixels at a time with SSE2, without the averaging of box_average, giving
// what the plain C below it gives to the bit.
static void same_size(const uint8_t *src, int sstride, uint8_t *Y, uint8_t *U, uint8_t *V, uint8_t *UV, int dw, int y0, int y1) {
	int cw = dw / 2;
	const __m128i ku = _mm_set_epi16(0, -38, -74, 112, 0, -38, -74, 112);
	const __m128i kv = _mm_set_epi16(0, 112, -94, -18, 0, 112, -94, -18);
	const __m128i two = _mm_set1_epi16(2);
	for (int y = y0; y < y1; y += 2) {
		const uint8_t *a = src + (size_t)y * sstride, *c = a + sstride;
		uint8_t *ya = Y + (size_t)y * dw, *yc = ya + dw;
		int x = 0;
		for (; lc_simd && x + 4 <= cw; x += 4) { // 8 pixels of each row
			__m128i a0 = _mm_loadu_si128((const __m128i *)(a + x * 8));
			__m128i a1 = _mm_loadu_si128((const __m128i *)(a + x * 8 + 16));
			__m128i c0 = _mm_loadu_si128((const __m128i *)(c + x * 8));
			__m128i c1 = _mm_loadu_si128((const __m128i *)(c + x * 8 + 16));
			__m128i ylo = _mm_packs_epi32(luma4(a0), luma4(a1));
			__m128i yhi = _mm_packs_epi32(luma4(c0), luma4(c1));
			__m128i yy = _mm_packus_epi16(ylo, yhi);
			_mm_storel_epi64((__m128i *)(ya + x * 2), yy);
			_mm_storel_epi64((__m128i *)(yc + x * 2), _mm_srli_si128(yy, 8));
			// Blocks 0, 1 and 2, 3: sums of four pixels, rounded averages.
			__m128i b01 = _mm_srli_epi16(_mm_add_epi16(sum2x2(a0, c0), two), 2);
			__m128i b23 = _mm_srli_epi16(_mm_add_epi16(sum2x2(a1, c1), two), 2);
			__m128i u = _mm_unpacklo_epi64(chroma2(b01, ku), chroma2(b23, ku)); // 4 x 32-bit
			__m128i v = _mm_unpacklo_epi64(chroma2(b01, kv), chroma2(b23, kv));
			__m128i u8 = _mm_packus_epi16(_mm_packs_epi32(u, u), _mm_setzero_si128());
			__m128i v8 = _mm_packus_epi16(_mm_packs_epi32(v, v), _mm_setzero_si128());
			int cy = y / 2;
			if (UV) {
				__m128i uv = _mm_unpacklo_epi8(u8, v8);
				_mm_storel_epi64((__m128i *)(UV + (size_t)cy * cw * 2 + x * 2), uv);
			} else {
				*(int32_t *)(U + (size_t)cy * cw + x) = _mm_cvtsi128_si32(u8);
				*(int32_t *)(V + (size_t)cy * cw + x) = _mm_cvtsi128_si32(v8);
			}
		}
		for (; x < cw; x++) {
			const uint8_t *p = a + x * 8, *q = c + x * 8;
			ya[x * 2] = LC_Y(p[2], p[1], p[0]);
			ya[x * 2 + 1] = LC_Y(p[6], p[5], p[4]);
			yc[x * 2] = LC_Y(q[2], q[1], q[0]);
			yc[x * 2 + 1] = LC_Y(q[6], q[5], q[4]);
			int r = (p[2] + p[6] + q[2] + q[6] + 2) >> 2;
			int g = (p[1] + p[5] + q[1] + q[5] + 2) >> 2;
			int b = (p[0] + p[4] + q[0] + q[4] + 2) >> 2;
			put_uv(U, V, UV, cw, y / 2, x, r, g, b);
		}
	}
}

void lc_bgra_to_yuv(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh, int nv12, int y0, int y1) {
	y0 &= ~1;
	y1 = y1 > dh ? dh : y1;
	uint8_t *Y = dst, *U = NULL, *V = NULL, *UV = NULL;
	if (nv12) {
		UV = dst + (size_t)dw * dh;
	} else {
		U = dst + (size_t)dw * dh;
		V = U + (size_t)(dw / 2) * (dh / 2);
	}
	if (sw == dw && sh == dh) {
		same_size(src, sstride, Y, U, V, UV, dw, y0, y1);
		return;
	}
	int *xs = bounds(sw, dw), *ys = bounds(sh, dh);
	int *row = malloc(sizeof(int) * dw * 6);
	for (int y = y0; y < y1; y++) {
		int *cur = row + (y & 1) * dw * 3;
		for (int x = 0; x < dw; x++) {
			int r, g, b;
			box_average(src, sstride, xs, ys, x, y, &r, &g, &b);
			cur[x * 3] = r;
			cur[x * 3 + 1] = g;
			cur[x * 3 + 2] = b;
			Y[(size_t)y * dw + x] = LC_Y(r, g, b);
		}
		if (y & 1) {
			int *a = row, *c = row + dw * 3;
			for (int x = 0; x < dw / 2; x++) {
				int i = x * 6;
				int r = (a[i] + a[i + 3] + c[i] + c[i + 3]) / 4;
				int g = (a[i + 1] + a[i + 4] + c[i + 1] + c[i + 4]) / 4;
				int b = (a[i + 2] + a[i + 5] + c[i + 2] + c[i + 5]) / 4;
				put_uv(U, V, UV, dw / 2, y / 2, x, r, g, b);
			}
		}
	}
	free(row);
	free(xs);
	free(ys);
}

void lc_bgra_to_i420(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh) {
	lc_bgra_to_yuv(src, sw, sh, sstride, dst, dw, dh, 0, 0, dh);
}

void lc_bgra_to_rgba(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh) {
	int *xs = bounds(sw, dw), *ys = bounds(sh, dh);
	for (int y = 0; y < dh; y++) {
		for (int x = 0; x < dw; x++) {
			int r, g, b;
			box_average(src, sstride, xs, ys, x, y, &r, &g, &b);
			uint8_t *p = dst + ((size_t)y * dw + x) * 4;
			p[0] = (uint8_t)r;
			p[1] = (uint8_t)g;
			p[2] = (uint8_t)b;
			p[3] = 255;
		}
	}
	free(xs);
	free(ys);
}

// ---- Capture ---------------------------------------------------------------

struct lc_capture {
	HWND hwnd;
	int x, y, w, h;
	int cursor;
	// dupl duplicates the screen, unless nodupl, where GDI captures it.
	lc_dupl *dupl;
	int nodupl;
	// For a window: dmx, dmy is the top left of the screen dupl
	// duplicates, set when dmset.
	int dmx, dmy, dmset;
	HDC mem;
	HBITMAP bmp;
	HGDIOBJ old;
	uint8_t *bits;
	int bw, bh;
	// changed: the last grab differs from the one before. seq is the
	// duplication's picture in bits, and cur the pointer drawn over it, at
	// cx, cy and cw by ch, if drawn.
	int changed;
	int seq;
	int drawn;
	HCURSOR cur;
	int cx, cy, cw, ch;
};

lc_capture *lc_capture_open(uintptr_t hwnd, int x, int y, int w, int h, int cursor) {
	lc_capture *c = calloc(1, sizeof *c);
	c->hwnd = (HWND)hwnd;
	c->x = x;
	c->y = y;
	c->w = w;
	c->h = h;
	c->cursor = cursor;
	return c;
}

// ensure makes the bitmap w by h pixels.
static int ensure(lc_capture *c, HDC ref, int w, int h) {
	if (c->bmp && c->bw == w && c->bh == h) {
		return 1;
	}
	if (c->bmp) {
		SelectObject(c->mem, c->old);
		DeleteObject(c->bmp);
		c->bmp = NULL;
	}
	if (!c->mem) {
		c->mem = CreateCompatibleDC(ref);
	}
	BITMAPINFO bi;
	memset(&bi, 0, sizeof bi);
	bi.bmiHeader.biSize = sizeof bi.bmiHeader;
	bi.bmiHeader.biWidth = w;
	bi.bmiHeader.biHeight = -h; // top row first
	bi.bmiHeader.biPlanes = 1;
	bi.bmiHeader.biBitCount = 32;
	bi.bmiHeader.biCompression = BI_RGB;
	void *bits = NULL;
	c->bmp = CreateDIBSection(ref, &bi, DIB_RGB_COLORS, &bits, NULL, 0);
	if (!c->bmp) {
		return 0;
	}
	c->bits = bits;
	c->old = SelectObject(c->mem, c->bmp);
	c->bw = w;
	c->bh = h;
	return 1;
}

// pointer finds the mouse pointer: its cursor, and where and how big it is
// over the area at ox, oy. It returns 0 when none shows there.
static int pointer(lc_capture *c, int ox, int oy, HCURSOR *cur, int *x, int *y, int *w, int *h) {
	CURSORINFO ci;
	memset(&ci, 0, sizeof ci);
	ci.cbSize = sizeof ci;
	if (!GetCursorInfo(&ci) || !(ci.flags & CURSOR_SHOWING) || !ci.hCursor) {
		return 0;
	}
	ICONINFO ii;
	if (!GetIconInfo(ci.hCursor, &ii)) {
		return 0;
	}
	*x = ci.ptScreenPos.x - ox - (int)ii.xHotspot;
	*y = ci.ptScreenPos.y - oy - (int)ii.yHotspot;
	BITMAP bm;
	memset(&bm, 0, sizeof bm);
	*w = *h = 64;
	if (ii.hbmColor && GetObject(ii.hbmColor, sizeof bm, &bm)) {
		*w = bm.bmWidth;
		*h = bm.bmHeight;
	} else if (ii.hbmMask && GetObject(ii.hbmMask, sizeof bm, &bm)) {
		*w = bm.bmWidth;
		*h = bm.bmHeight / 2; // a black and white pointer's mask is two high
	}
	if (ii.hbmMask) {
		DeleteObject(ii.hbmMask);
	}
	if (ii.hbmColor) {
		DeleteObject(ii.hbmColor);
	}
	*cur = ci.hCursor;
	return !(*x >= c->bw || *y >= c->bh || *x + *w <= 0 || *y + *h <= 0);
}

// draw_cursor draws the mouse pointer, if it is over the area at ox, oy,
// and notes where.
static void draw_cursor(lc_capture *c, int ox, int oy) {
	HCURSOR cur;
	int x, y, w, h;
	c->drawn = 0;
	if (!pointer(c, ox, oy, &cur, &x, &y, &w, &h)) {
		return;
	}
	DrawIconEx(c->mem, x, y, cur, 0, 0, 0, NULL, DI_NORMAL);
	c->drawn = 1;
	c->cur = cur;
	c->cx = x;
	c->cy = y;
	c->cw = w;
	c->ch = h;
}

// pointer_moved reports whether the pointer is no longer as drawn last.
static int pointer_moved(lc_capture *c, int ox, int oy) {
	HCURSOR cur;
	int x, y, w, h;
	int shows = pointer(c, ox, oy, &cur, &x, &y, &w, &h);
	if (!shows || !c->drawn) {
		return shows != c->drawn;
	}
	return cur != c->cur || x != c->cx || y != c->cy;
}

// unpaint puts back, from the clean picture pix (rows pw*4 bytes apart),
// what the pointer last drawn covers.
static void unpaint(lc_capture *c, const uint8_t *pix, int pw) {
	int x0 = c->cx < 0 ? 0 : c->cx, y0 = c->cy < 0 ? 0 : c->cy;
	int x1 = c->cx + c->cw > c->bw ? c->bw : c->cx + c->cw;
	int y1 = c->cy + c->ch > c->bh ? c->bh : c->cy + c->ch;
	for (int y = y0; y < y1; y++) {
		memcpy(c->bits + ((size_t)y * c->bw + x0) * 4, pix + ((size_t)y * pw + x0) * 4, (size_t)(x1 - x0) * 4);
	}
	c->drawn = 0;
}

// dpi_aware has the thread see the screens in their own pixels, rather than
// scaled as for programs older than scaling, and returns how it saw them,
// for dpi_restore. Before Windows 10, the thread sees them as the process.
typedef HANDLE(WINAPI *set_dpi_context_fn)(HANDLE);

HANDLE lc_dpi_aware(void) {
	static set_dpi_context_fn set;
	static int loaded;
	if (!loaded) {
		loaded = 1;
		set = (set_dpi_context_fn)GetProcAddress(GetModuleHandleW(L"user32.dll"), "SetThreadDpiAwarenessContext");
	}
	return set ? set((HANDLE)(intptr_t)-4) : NULL; // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
}

void lc_dpi_restore(HANDLE old) {
	if (old) {
		((set_dpi_context_fn)GetProcAddress(GetModuleHandleW(L"user32.dll"), "SetThreadDpiAwarenessContext"))(old);
	}
}

static const uint8_t *grab(lc_capture *c, int *w, int *h, int *stride, int *gone);

const uint8_t *lc_capture_grab(lc_capture *c, int *w, int *h, int *stride, int *gone, int *changed) {
	// Screens are captured in their own pixels, as the pointer is placed.
	HANDLE old = lc_dpi_aware();
	c->changed = 1;
	const uint8_t *pix = grab(c, w, h, stride, gone);
	lc_dpi_restore(old);
	*changed = c->changed;
	return pix;
}

// gdi_screen captures the screen with GDI, slower than duplication but
// working everywhere. It reports whether it could.
static int gdi_screen(lc_capture *c) {
	// The screen may have changed resolution since it was chosen.
	POINT at = {c->x, c->y};
	MONITORINFO mi;
	memset(&mi, 0, sizeof mi);
	mi.cbSize = sizeof mi;
	if (GetMonitorInfoW(MonitorFromPoint(at, MONITOR_DEFAULTTONEAREST), &mi)) {
		c->w = mi.rcMonitor.right - mi.rcMonitor.left;
		c->h = mi.rcMonitor.bottom - mi.rcMonitor.top;
	}
	HDC sdc = GetDC(NULL);
	if (!ensure(c, sdc, c->w, c->h)) {
		ReleaseDC(NULL, sdc);
		return 0;
	}
	BitBlt(c->mem, 0, 0, c->w, c->h, sdc, c->x, c->y, SRCCOPY);
	ReleaseDC(NULL, sdc);
	if (c->cursor) {
		draw_cursor(c, c->x, c->y);
	}
	return 1;
}

// uncovered reports whether the window shows on the screen at the points of
// its client area at sx, sy, w by h, unhidden by any other window.
static int uncovered(HWND hwnd, int sx, int sy, int w, int h) {
	HWND root = GetAncestor(hwnd, GA_ROOT);
	int xs[3] = {sx + 2, sx + w / 2, sx + w - 3};
	int ys[3] = {sy + 2, sy + h / 2, sy + h - 3};
	for (int i = 0; i < 3; i++) {
		for (int j = 0; j < 3; j++) {
			POINT p = {xs[i], ys[j]};
			HWND at = WindowFromPoint(p);
			if (!at || GetAncestor(at, GA_ROOT) != root) {
				return 0;
			}
		}
	}
	return 1;
}

int lc_window_uncovered(uintptr_t hwnd, int sx, int sy, int w, int h) {
	return uncovered((HWND)hwnd, sx, sy, w, h);
}

// window_from_screen copies the window's client area, at sx, sy, w by h,
// from a duplication of its screen: many times faster than PrintWindow,
// which takes tens of milliseconds a picture for games and browsers, and
// capped such streams near 10 pictures a second. It reports whether it
// could; the window must be uncovered and wholly on one screen.
static int window_from_screen(lc_capture *c, int sx, int sy, int w, int h) {
	if (c->nodupl || !uncovered(c->hwnd, sx, sy, w, h)) {
		return 0;
	}
	MONITORINFO mi;
	memset(&mi, 0, sizeof mi);
	mi.cbSize = sizeof mi;
	if (!GetMonitorInfoW(MonitorFromWindow(c->hwnd, MONITOR_DEFAULTTONEAREST), &mi)) {
		return 0;
	}
	RECT m = mi.rcMonitor;
	if (sx < m.left || sy < m.top || sx + w > m.right || sy + h > m.bottom) {
		return 0;
	}
	// The window moved to another screen: that one is duplicated.
	if (c->dupl && (!c->dmset || c->dmx != m.left || c->dmy != m.top)) {
		lc_dupl_close(c->dupl);
		c->dupl = NULL;
	}
	if (!c->dupl) {
		c->dupl = lc_dupl_open(m.left, m.top);
		c->dmset = 1;
		c->dmx = m.left;
		c->dmy = m.top;
	}
	const uint8_t *pix;
	int pw, ph;
	int r = lc_dupl_grab(c->dupl, &pix, &pw, &ph);
	if (r < 0) {
		lc_dupl_close(c->dupl);
		c->dupl = NULL;
		c->nodupl = 1;
		return 0;
	}
	if (r == 0) {
		return 0;
	}
	int ox = sx - m.left, oy = sy - m.top;
	if (ox < 0 || oy < 0 || ox + w > pw || oy + h > ph) {
		return 0;
	}
	HDC sdc = GetDC(NULL);
	int ok = ensure(c, sdc, w, h);
	ReleaseDC(NULL, sdc);
	if (!ok) {
		return 0;
	}
	for (int y = 0; y < h; y++) {
		memcpy(c->bits + (size_t)y * w * 4, pix + ((size_t)(oy + y) * pw + ox) * 4, (size_t)w * 4);
	}
	return 1;
}

static const uint8_t *grab(lc_capture *c, int *w, int *h, int *stride, int *gone) {
	*gone = 0;
	if (c->hwnd) {
		if (!IsWindow(c->hwnd)) {
			*gone = 1;
			return NULL;
		}
		if (IsIconic(c->hwnd)) {
			return NULL;
		}
		RECT r;
		if (!GetClientRect(c->hwnd, &r) || r.right <= 0 || r.bottom <= 0) {
			return NULL;
		}
		POINT so = {0, 0};
		ClientToScreen(c->hwnd, &so);
		if (window_from_screen(c, so.x, so.y, r.right, r.bottom)) {
			if (c->cursor) {
				draw_cursor(c, so.x, so.y);
			}
			goto done;
		}
		HDC wdc = GetDC(c->hwnd);
		if (!ensure(c, wdc, r.right, r.bottom)) {
			ReleaseDC(c->hwnd, wdc);
			return NULL;
		}
		// What the window draws itself, even under other windows: with all
		// of it on Windows 8.1 and later, and with GDI's view of it on 7.
		if (!PrintWindow(c->hwnd, c->mem, PW_CLIENTONLY | 2)) {
			if (!PrintWindow(c->hwnd, c->mem, PW_CLIENTONLY)) {
				BitBlt(c->mem, 0, 0, r.right, r.bottom, wdc, 0, 0, SRCCOPY);
			}
		}
		ReleaseDC(c->hwnd, wdc);
		if (c->cursor) {
			POINT o = {0, 0};
			ClientToScreen(c->hwnd, &o);
			draw_cursor(c, o.x, o.y);
		}
	} else if (!c->nodupl) {
		if (!c->dupl) {
			c->dupl = lc_dupl_open(c->x, c->y);
		}
		const uint8_t *pix;
		int pw, ph;
		int r = lc_dupl_grab(c->dupl, &pix, &pw, &ph);
		if (r < 0) {
			lc_dupl_close(c->dupl);
			c->dupl = NULL;
			c->nodupl = 1;
			return grab(c, w, h, stride, gone);
		}
		if (r == 0) {
			// Nothing drawn since the duplication began, which would be
			// black: GDI's picture stands in.
			if (!gdi_screen(c)) {
				return NULL;
			}
		} else {
			int fresh = !(c->bmp && c->bw == pw && c->bh == ph);
			HDC sdc = GetDC(NULL);
			int ok = ensure(c, sdc, pw, ph);
			ReleaseDC(NULL, sdc);
			if (!ok) {
				return NULL;
			}
			// The duplication keeps the picture clean of the pointer, drawn
			// over a copy. The copy is only made again when the screen
			// changed; when only the pointer moved, the little it covered
			// is put back and it is drawn where it is now; when neither,
			// the picture is the same as the last.
			int seq = lc_dupl_seq(c->dupl);
			if (fresh || seq != c->seq) {
				memcpy(c->bits, pix, (size_t)pw * ph * 4);
				c->seq = seq;
				c->drawn = 0;
				if (c->cursor) {
					draw_cursor(c, c->x, c->y);
				}
			} else if (c->cursor && pointer_moved(c, c->x, c->y)) {
				if (c->drawn) {
					unpaint(c, pix, pw);
				}
				draw_cursor(c, c->x, c->y);
			} else {
				c->changed = 0;
			}
		}
	} else if (!gdi_screen(c)) {
		return NULL;
	}
done:
	GdiFlush();
	*w = c->bw;
	*h = c->bh;
	*stride = c->bw * 4;
	return c->bits;
}

int lc_capture_duplicates(lc_capture *c) {
	return c->dupl != NULL;
}

void lc_capture_release(lc_capture *c) {
	lc_dupl_close(c->dupl);
	c->dupl = NULL;
	c->dmset = 0;
}

void lc_capture_close(lc_capture *c) {
	if (!c) {
		return;
	}
	if (c->bmp) {
		SelectObject(c->mem, c->old);
		DeleteObject(c->bmp);
	}
	if (c->mem) {
		DeleteDC(c->mem);
	}
	lc_dupl_close(c->dupl);
	free(c);
}

// ---- Sources ---------------------------------------------------------------

typedef struct {
	lc_source *out;
	int max, n;
	uintptr_t self;
} enum_state;

static BOOL CALLBACK monitor_proc(HMONITOR m, HDC dc, LPRECT rect, LPARAM lp) {
	enum_state *s = (enum_state *)lp;
	if (s->n >= s->max) {
		return FALSE;
	}
	MONITORINFOEXW mi;
	memset(&mi, 0, sizeof mi);
	mi.cbSize = sizeof mi;
	if (!GetMonitorInfoW(m, (MONITORINFO *)&mi)) {
		return TRUE;
	}
	lc_source *src = &s->out[s->n++];
	memset(src, 0, sizeof *src);
	src->x = mi.rcMonitor.left;
	src->y = mi.rcMonitor.top;
	src->w = mi.rcMonitor.right - mi.rcMonitor.left;
	src->h = mi.rcMonitor.bottom - mi.rcMonitor.top;
	src->primary = (mi.dwFlags & MONITORINFOF_PRIMARY) != 0;
	return TRUE;
}

int lc_list_monitors(lc_source *out, int max) {
	enum_state s = {out, max, 0, 0};
	// In the screens' own pixels, as they are captured.
	HANDLE old = lc_dpi_aware();
	EnumDisplayMonitors(NULL, NULL, monitor_proc, (LPARAM)&s);
	lc_dpi_restore(old);
	return s.n;
}

typedef HRESULT(WINAPI *dwm_get_fn)(HWND, DWORD, PVOID, DWORD);

// cloaked reports whether Windows hides the window, as it does for those of
// other virtual desktops and for suspended apps. Windows 7 has no such thing.
static int cloaked(HWND w) {
	static dwm_get_fn get;
	static int loaded;
	if (!loaded) {
		loaded = 1;
		HMODULE dwm = LoadLibraryW(L"dwmapi.dll");
		if (dwm) {
			get = (dwm_get_fn)GetProcAddress(dwm, "DwmGetWindowAttribute");
		}
	}
	if (!get) {
		return 0;
	}
	DWORD c = 0;
	return get(w, 14 /* DWMWA_CLOAKED */, &c, sizeof c) == S_OK && c != 0;
}

static BOOL CALLBACK window_proc(HWND w, LPARAM lp) {
	enum_state *s = (enum_state *)lp;
	if (s->n >= s->max) {
		return FALSE;
	}
	if ((uintptr_t)w == s->self || !IsWindowVisible(w) || IsIconic(w)) {
		return TRUE;
	}
	// Windows of their own, not tool windows or the parts of others.
	if (GetAncestor(w, GA_ROOTOWNER) != w && GetWindow(w, GW_OWNER) != NULL) {
		return TRUE;
	}
	LONG ex = GetWindowLongW(w, GWL_EXSTYLE);
	if (ex & WS_EX_TOOLWINDOW) {
		return TRUE;
	}
	if (cloaked(w)) {
		return TRUE;
	}
	wchar_t cls[64];
	if (GetClassNameW(w, cls, 64) && (wcscmp(cls, L"Progman") == 0 || wcscmp(cls, L"Shell_TrayWnd") == 0 || wcscmp(cls, L"WorkerW") == 0)) {
		return TRUE;
	}
	RECT r;
	if (!GetClientRect(w, &r) || r.right < 64 || r.bottom < 64) {
		return TRUE;
	}
	lc_source *src = &s->out[s->n];
	memset(src, 0, sizeof *src);
	if (GetWindowTextW(w, (wchar_t *)src->title, 255) <= 0) {
		return TRUE;
	}
	src->hwnd = (uintptr_t)w;
	src->w = r.right;
	src->h = r.bottom;
	s->n++;
	return TRUE;
}

int lc_list_windows(lc_source *out, int max, uintptr_t self) {
	enum_state s = {out, max, 0, self};
	EnumWindows(window_proc, (LPARAM)&s);
	return s.n;
}
