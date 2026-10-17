//go:build windows && !no_voice

#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdlib.h>
#include <string.h>

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
	p.iUsageType = SCREEN_CONTENT_REAL_TIME;
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
	p.bEnableFrameSkip = true;
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
	*r = sr / n;
	*g = sg / n;
	*b = sb / n;
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

void lc_bgra_to_i420(const uint8_t *src, int sw, int sh, int sstride, uint8_t *dst, int dw, int dh) {
	int *xs = bounds(sw, dw), *ys = bounds(sh, dh);
	int *row = malloc(sizeof(int) * dw * 6);
	uint8_t *Y = dst, *U = dst + dw * dh, *V = U + (dw / 2) * (dh / 2);
	for (int y = 0; y < dh; y++) {
		int *cur = row + (y & 1) * dw * 3;
		for (int x = 0; x < dw; x++) {
			int r, g, b;
			box_average(src, sstride, xs, ys, x, y, &r, &g, &b);
			cur[x * 3] = r;
			cur[x * 3 + 1] = g;
			cur[x * 3 + 2] = b;
			Y[y * dw + x] = (uint8_t)(((66 * r + 129 * g + 25 * b + 128) >> 8) + 16);
		}
		if (y & 1) {
			int *a = row, *c = row + dw * 3;
			for (int x = 0; x < dw / 2; x++) {
				int i = x * 6;
				int r = (a[i] + a[i + 3] + c[i] + c[i + 3]) / 4;
				int g = (a[i + 1] + a[i + 4] + c[i + 1] + c[i + 4]) / 4;
				int b = (a[i + 2] + a[i + 5] + c[i + 2] + c[i + 5]) / 4;
				U[(y / 2) * (dw / 2) + x] = (uint8_t)(((-38 * r - 74 * g + 112 * b + 128) >> 8) + 128);
				V[(y / 2) * (dw / 2) + x] = (uint8_t)(((112 * r - 94 * g - 18 * b + 128) >> 8) + 128);
			}
		}
	}
	free(row);
	free(xs);
	free(ys);
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

// draw_cursor draws the mouse pointer, if it is over the area at ox, oy.
static void draw_cursor(lc_capture *c, int ox, int oy) {
	CURSORINFO ci;
	memset(&ci, 0, sizeof ci);
	ci.cbSize = sizeof ci;
	if (!GetCursorInfo(&ci) || !(ci.flags & CURSOR_SHOWING) || !ci.hCursor) {
		return;
	}
	ICONINFO ii;
	if (!GetIconInfo(ci.hCursor, &ii)) {
		return;
	}
	int x = ci.ptScreenPos.x - ox - (int)ii.xHotspot;
	int y = ci.ptScreenPos.y - oy - (int)ii.yHotspot;
	if (ii.hbmMask) {
		DeleteObject(ii.hbmMask);
	}
	if (ii.hbmColor) {
		DeleteObject(ii.hbmColor);
	}
	if (x > c->bw || y > c->bh || x < -64 || y < -64) {
		return;
	}
	DrawIconEx(c->mem, x, y, ci.hCursor, 0, 0, 0, NULL, DI_NORMAL);
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

const uint8_t *lc_capture_grab(lc_capture *c, int *w, int *h, int *stride, int *gone) {
	// Screens are captured in their own pixels, as the pointer is placed.
	HANDLE old = lc_dpi_aware();
	const uint8_t *pix = grab(c, w, h, stride, gone);
	lc_dpi_restore(old);
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
			HDC sdc = GetDC(NULL);
			int ok = ensure(c, sdc, pw, ph);
			ReleaseDC(NULL, sdc);
			if (!ok) {
				return NULL;
			}
			// The picture is kept clean of the pointer, drawn over a copy.
			memcpy(c->bits, pix, (size_t)pw * ph * 4);
			if (c->cursor) {
				draw_cursor(c, c->x, c->y);
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
