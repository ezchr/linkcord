//go:build windows && !no_voice

// Capturing a screen with DXGI's desktop duplication, which Windows 8 and
// later have. Unlike GDI, it sees games in full screen, and follows the
// screen when they change its resolution.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <dxgi1_2.h>
#include <stdlib.h>
#include <string.h>

#include "native.h"

// The interfaces asked for, written out rather than taken from dxguid, which
// would have to be linked.
static const GUID lc_IID_IDXGIFactory1 = {0x770aae78, 0xf26f, 0x4dba, {0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}};
static const GUID lc_IID_IDXGIOutput1 = {0x00cddea8, 0x939b, 0x4b83, {0xa3, 0x40, 0xa6, 0x85, 0x22, 0x66, 0x66, 0xcc}};
static const GUID lc_IID_ID3D11Texture2D = {0x6f15aaf2, 0xd208, 0x4e89, {0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}};
static const GUID lc_IID_ID3D11VideoDevice = {0x10ec4d5b, 0x975a, 0x4689, {0xb9, 0xe4, 0xd0, 0xaa, 0xc3, 0x0f, 0xe3, 0x33}};
static const GUID lc_IID_ID3D11VideoContext = {0x61f21c45, 0x3c0e, 0x4a74, {0x9c, 0xea, 0x67, 0x10, 0x0d, 0x9a, 0xd5, 0xe4}};
static const GUID lc_IID_ID3D10Multithread = {0x9b7e4e00, 0x342c, 0x4106, {0xa1, 0x9f, 0x4f, 0x27, 0x04, 0xf6, 0x89, 0xf0}};

// lc_mt_protect has the device take calls from several threads, one at a
// time, as Media Foundation needs: ID3D10Multithread, cut down to that call.
typedef struct lc_mt lc_mt;
typedef struct {
	HRESULT(STDMETHODCALLTYPE *QueryInterface)(lc_mt *, REFIID, void **);
	ULONG(STDMETHODCALLTYPE *AddRef)(lc_mt *);
	ULONG(STDMETHODCALLTYPE *Release)(lc_mt *);
	void(STDMETHODCALLTYPE *Enter)(lc_mt *);
	void(STDMETHODCALLTYPE *Leave)(lc_mt *);
	BOOL(STDMETHODCALLTYPE *SetMultithreadProtected)(lc_mt *, BOOL);
} lc_mt_vtbl;
struct lc_mt {
	const lc_mt_vtbl *lpVtbl;
};

void lc_mt_protect(void *device) {
	IUnknown *dev = device;
	lc_mt *mt = NULL;
	if (SUCCEEDED(dev->lpVtbl->QueryInterface(dev, &lc_IID_ID3D10Multithread, (void **)&mt)) && mt) {
		mt->lpVtbl->SetMultithreadProtected(mt, TRUE);
		mt->lpVtbl->Release(mt);
	}
}

typedef HRESULT(WINAPI *create_factory_fn)(REFIID, void **);
typedef HRESULT(WINAPI *create_device_fn)(IDXGIAdapter *, D3D_DRIVER_TYPE, HMODULE, UINT, const D3D_FEATURE_LEVEL *, UINT, UINT, ID3D11Device **, D3D_FEATURE_LEVEL *, ID3D11DeviceContext **);

// The libraries are loaded when first needed, so that Linkcord starts where
// they are missing.
static create_factory_fn create_factory;
static create_device_fn create_device;

static int load(void) {
	static int loaded;
	if (!loaded) {
		loaded = 1;
		HMODULE dxgi = LoadLibraryW(L"dxgi.dll");
		HMODULE d3d = LoadLibraryW(L"d3d11.dll");
		if (dxgi) {
			create_factory = (create_factory_fn)GetProcAddress(dxgi, "CreateDXGIFactory1");
		}
		if (d3d) {
			create_device = (create_device_fn)GetProcAddress(d3d, "D3D11CreateDevice");
		}
	}
	return create_factory && create_device;
}

struct lc_dupl {
	// x and y are the top left of the screen on the desktop.
	int x, y;
	ID3D11Device *dev;
	ID3D11DeviceContext *ctx;
	IDXGIOutputDuplication *dup;
	ID3D11Texture2D *staging;
	int sw, sh;
	// frame is the last picture, BGRA, fw by fh with rows fw*4 bytes apart.
	uint8_t *frame;
	int fw, fh;
	int have;
	// seq counts the pictures kept in frame.
	int seq;
	// With gpu, pictures are kept in the texture gframe rather than read
	// back into frame: the graphics card converts and encodes them. gen
	// counts the devices made, which a texture of an older one is not of.
	int gpu;
	ID3D11Texture2D *gframe;
	int gen;
	// worked reports whether the screen was ever duplicated, and retry when
	// to try again after it stopped, as it does when a game changes the
	// resolution or Windows shows its secure desktop.
	int worked;
	DWORD retry;
};

static void release(lc_dupl *d) {
	if (d->gframe) {
		ID3D11Texture2D_Release(d->gframe);
		d->gframe = NULL;
	}
	if (d->staging) {
		ID3D11Texture2D_Release(d->staging);
		d->staging = NULL;
	}
	if (d->dup) {
		IDXGIOutputDuplication_Release(d->dup);
		d->dup = NULL;
	}
	if (d->ctx) {
		ID3D11DeviceContext_Release(d->ctx);
		d->ctx = NULL;
	}
	if (d->dev) {
		ID3D11Device_Release(d->dev);
		d->dev = NULL;
	}
	d->sw = d->sh = 0;
}

// duplicate starts duplicating the screen at d->x, d->y, on the graphics
// card that shows it.
static HRESULT duplicate(lc_dupl *d) {
	if (!load()) {
		return E_NOTIMPL;
	}
	IDXGIFactory1 *f = NULL;
	HRESULT hr = create_factory(&lc_IID_IDXGIFactory1, (void **)&f);
	if (FAILED(hr)) {
		return hr;
	}
	hr = DXGI_ERROR_NOT_FOUND;
	IDXGIAdapter1 *a = NULL;
	for (UINT i = 0; hr == DXGI_ERROR_NOT_FOUND && IDXGIFactory1_EnumAdapters1(f, i, &a) == S_OK; i++) {
		IDXGIOutput *o = NULL;
		for (UINT j = 0; IDXGIAdapter1_EnumOutputs(a, j, &o) == S_OK; j++) {
			DXGI_OUTPUT_DESC od;
			if (FAILED(IDXGIOutput_GetDesc(o, &od)) || !od.AttachedToDesktop || od.DesktopCoordinates.left != d->x || od.DesktopCoordinates.top != d->y) {
				IDXGIOutput_Release(o);
				continue;
			}
			// Video support for the graphics card's converting and encoding,
			// where the device can have it.
			hr = create_device((IDXGIAdapter *)a, D3D_DRIVER_TYPE_UNKNOWN, NULL, D3D11_CREATE_DEVICE_VIDEO_SUPPORT | D3D11_CREATE_DEVICE_BGRA_SUPPORT, NULL, 0, D3D11_SDK_VERSION, &d->dev, NULL, &d->ctx);
			if (FAILED(hr)) {
				hr = create_device((IDXGIAdapter *)a, D3D_DRIVER_TYPE_UNKNOWN, NULL, 0, NULL, 0, D3D11_SDK_VERSION, &d->dev, NULL, &d->ctx);
			}
			if (SUCCEEDED(hr)) {
				d->gen++;
				// The encoder's threads use the device too.
				lc_mt_protect((IUnknown *)d->dev);
			}
			IDXGIOutput1 *o1 = NULL;
			if (SUCCEEDED(hr)) {
				hr = IDXGIOutput_QueryInterface(o, &lc_IID_IDXGIOutput1, (void **)&o1);
			}
			if (SUCCEEDED(hr)) {
				// Windows duplicates the screen for threads aware of the DPI
				// of each screen only.
				HANDLE old = lc_dpi_aware();
				hr = IDXGIOutput1_DuplicateOutput(o1, (IUnknown *)d->dev, &d->dup);
				lc_dpi_restore(old);
				IDXGIOutput1_Release(o1);
			}
			IDXGIOutput_Release(o);
			break;
		}
		IDXGIAdapter1_Release(a);
	}
	IDXGIFactory1_Release(f);
	if (FAILED(hr)) {
		release(d);
	}
	return hr;
}

lc_dupl *lc_dupl_open(int x, int y) {
	lc_dupl *d = calloc(1, sizeof *d);
	d->x = x;
	d->y = y;
	return d;
}

// keep copies the picture of tex into d->frame.
static int keep(lc_dupl *d, ID3D11Texture2D *tex) {
	D3D11_TEXTURE2D_DESC desc;
	ID3D11Texture2D_GetDesc(tex, &desc);
	if (desc.Format != DXGI_FORMAT_B8G8R8A8_UNORM) {
		return -1;
	}
	int w = (int)desc.Width, h = (int)desc.Height;
	if (d->gpu) {
		// A copy on the graphics card: the duplication's own picture is
		// not to be held past ReleaseFrame.
		if (!d->gframe || d->fw != w || d->fh != h) {
			if (d->gframe) {
				ID3D11Texture2D_Release(d->gframe);
				d->gframe = NULL;
			}
			desc.Usage = D3D11_USAGE_DEFAULT;
			desc.BindFlags = D3D11_BIND_SHADER_RESOURCE;
			desc.CPUAccessFlags = 0;
			desc.MiscFlags = 0;
			desc.MipLevels = 1;
			desc.ArraySize = 1;
			desc.SampleDesc.Count = 1;
			desc.SampleDesc.Quality = 0;
			if (FAILED(ID3D11Device_CreateTexture2D(d->dev, &desc, NULL, &d->gframe))) {
				return 0;
			}
			d->fw = w;
			d->fh = h;
		}
		ID3D11DeviceContext_CopyResource(d->ctx, (ID3D11Resource *)d->gframe, (ID3D11Resource *)tex);
		d->have = 1;
		d->seq++;
		return 1;
	}
	if (!d->staging || d->sw != w || d->sh != h) {
		if (d->staging) {
			ID3D11Texture2D_Release(d->staging);
			d->staging = NULL;
		}
		desc.Usage = D3D11_USAGE_STAGING;
		desc.BindFlags = 0;
		desc.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
		desc.MiscFlags = 0;
		desc.MipLevels = 1;
		desc.ArraySize = 1;
		desc.SampleDesc.Count = 1;
		desc.SampleDesc.Quality = 0;
		if (FAILED(ID3D11Device_CreateTexture2D(d->dev, &desc, NULL, &d->staging))) {
			return 0;
		}
		d->sw = w;
		d->sh = h;
	}
	ID3D11DeviceContext_CopyResource(d->ctx, (ID3D11Resource *)d->staging, (ID3D11Resource *)tex);
	D3D11_MAPPED_SUBRESOURCE m;
	if (FAILED(ID3D11DeviceContext_Map(d->ctx, (ID3D11Resource *)d->staging, 0, D3D11_MAP_READ, 0, &m))) {
		return 0;
	}
	if (d->fw != w || d->fh != h) {
		free(d->frame);
		d->frame = malloc((size_t)w * h * 4);
		d->fw = w;
		d->fh = h;
	}
	for (int row = 0; row < h; row++) {
		memcpy(d->frame + (size_t)row * w * 4, (uint8_t *)m.pData + (size_t)row * m.RowPitch, (size_t)w * 4);
	}
	ID3D11DeviceContext_Unmap(d->ctx, (ID3D11Resource *)d->staging, 0);
	d->have = 1;
	d->seq++;
	return 1;
}

// next takes the next picture of the screen, if it changed within timeout
// milliseconds. It returns -1 when the screen cannot be duplicated, and 0
// otherwise.
static int next(lc_dupl *d, UINT timeout) {
	DXGI_OUTDUPL_FRAME_INFO info;
	IDXGIResource *res = NULL;
	HRESULT hr = IDXGIOutputDuplication_AcquireNextFrame(d->dup, timeout, &info, &res);
	if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
		// Nothing changed on the screen.
		return 0;
	}
	if (FAILED(hr)) {
		// The resolution changed, a game took the screen for itself, or the
		// secure desktop shows: duplicate it again.
		release(d);
		return 0;
	}
	int kept = 1;
	// A picture of only the pointer moving has nothing new, and the first
	// one, before anything was drawn, is black: what was drawn is waited
	// for, and GDI's picture stands in meanwhile.
	if (info.LastPresentTime.QuadPart != 0) {
		ID3D11Texture2D *tex = NULL;
		if (SUCCEEDED(IDXGIResource_QueryInterface(res, &lc_IID_ID3D11Texture2D, (void **)&tex))) {
			kept = keep(d, tex);
			ID3D11Texture2D_Release(tex);
		}
	}
	IDXGIResource_Release(res);
	IDXGIOutputDuplication_ReleaseFrame(d->dup);
	if (kept < 0) {
		release(d);
		return -1;
	}
	return 0;
}

// poll takes the latest picture of the screen, duplicating it again when
// it stopped. It returns 1 when there is a picture, 0 when there is none yet,
// and -1 when the screen cannot be duplicated.
static int poll(lc_dupl *d) {
	if (!d->dup) {
		if (d->worked && GetTickCount() - d->retry < 500) {
			goto last;
		}
		d->retry = GetTickCount();
		if (FAILED(duplicate(d))) {
			// Where it never worked, GDI captures instead.
			if (!d->worked) {
				return -1;
			}
			goto last;
		}
		d->worked = 1;
	}
	// The first picture is waited for; then what has not changed is kept.
	for (int i = 0; i < 5 && d->dup; i++) {
		if (next(d, d->have ? 0 : 100) < 0) {
			return -1;
		}
		if (d->have) {
			break;
		}
	}
last:
	return d->have ? 1 : 0;
}

int lc_dupl_grab(lc_dupl *d, const uint8_t **pix, int *w, int *h) {
	int r = poll(d);
	if (r <= 0) {
		return r;
	}
	*pix = d->frame;
	*w = d->fw;
	*h = d->fh;
	return 1;
}

int lc_dupl_seq(lc_dupl *d) {
	return d->seq;
}

void lc_dupl_close(lc_dupl *d) {
	if (!d) {
		return;
	}
	release(d);
	free(d->frame);
	free(d);
}

// ---- On the graphics card ---------------------------------------------------
// From Windows 8, a screen or an uncovered window is converted to NV12 by the
// graphics card's video processor and handed to its encoder as a texture: no
// picture is read back, copied or converted by the processor. The pointer is
// drawn over a copy, a little square at a time.

#define LC_GPU_SLOTS 8
#define LC_POINTER_MAX 256

struct lc_gpucap {
	uintptr_t hwnd;
	int x, y; // a screen's top left
	int cursor;
	lc_dupl *d;
	int dmx, dmy; // the top left of the screen d duplicates
	int devgen;   // d's device the textures below are of
	int gen;      // counts the devices used, for the encoder
	// comp is the picture with the pointer drawn, of the whole screen; the
	// crop sx, sy, sw by sh is what is shared.
	ID3D11Texture2D *comp;
	int cw, ch;
	int seq, drawn;
	HCURSOR cur;
	int px, py, pw, ph;
	int sx, sy, sw, sh;
	// The square the pointer is drawn in, read back and drawn by GDI.
	ID3D11Texture2D *pstage;
	HDC pdc;
	HBITMAP pbmp;
	HGDIOBJ pold;
	uint8_t *pbits;
	// The video processor, from comp to the NV12 textures slot.
	ID3D11VideoDevice *vdev;
	ID3D11VideoContext *vctx;
	ID3D11VideoProcessorEnumerator *venum;
	ID3D11VideoProcessor *vp;
	ID3D11VideoProcessorInputView *inview;
	int vw, vh, ow, oh;
	ID3D11Texture2D *slot[LC_GPU_SLOTS];
	ID3D11VideoProcessorOutputView *outview[LC_GPU_SLOTS];
	int next;
	// A copy read back now and then for the preview.
	ID3D11Texture2D *preview;
};

#define LC_REL(p, fn) \
	do { \
		if (p) { \
			fn(p); \
			(p) = NULL; \
		} \
	} while (0)

static void gpu_release_vp(lc_gpucap *g) {
	for (int i = 0; i < LC_GPU_SLOTS; i++) {
		LC_REL(g->outview[i], ID3D11VideoProcessorOutputView_Release);
		LC_REL(g->slot[i], ID3D11Texture2D_Release);
	}
	LC_REL(g->inview, ID3D11VideoProcessorInputView_Release);
	LC_REL(g->vp, ID3D11VideoProcessor_Release);
	LC_REL(g->venum, ID3D11VideoProcessorEnumerator_Release);
	g->vw = g->vh = g->ow = g->oh = 0;
}

// gpu_release lets go of everything of the device, as when it changed.
static void gpu_release(lc_gpucap *g) {
	gpu_release_vp(g);
	LC_REL(g->vctx, ID3D11VideoContext_Release);
	LC_REL(g->vdev, ID3D11VideoDevice_Release);
	LC_REL(g->comp, ID3D11Texture2D_Release);
	LC_REL(g->pstage, ID3D11Texture2D_Release);
	LC_REL(g->preview, ID3D11Texture2D_Release);
	g->cw = g->ch = 0;
	g->seq = -1;
	g->drawn = 0;
}

lc_gpucap *lc_gpucap_open(uintptr_t hwnd, int x, int y, int cursor) {
	if (!load()) {
		return NULL;
	}
	lc_gpucap *g = calloc(1, sizeof *g);
	g->hwnd = hwnd;
	g->x = x;
	g->y = y;
	g->cursor = cursor;
	g->seq = -1;
	g->devgen = -1;
	return g;
}

void lc_gpucap_close(lc_gpucap *g) {
	if (!g) {
		return;
	}
	gpu_release(g);
	if (g->pbmp) {
		SelectObject(g->pdc, g->pold);
		DeleteObject(g->pbmp);
	}
	if (g->pdc) {
		DeleteDC(g->pdc);
	}
	lc_dupl_close(g->d);
	free(g);
}

// gpu_pointer finds the pointer over the screen at ox, oy, as pointer in
// native_windows.c does.
static int gpu_pointer(int ox, int oy, int bw, int bh, HCURSOR *cur, int *x, int *y, int *w, int *h) {
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
		*h = bm.bmHeight / 2;
	}
	if (ii.hbmMask) {
		DeleteObject(ii.hbmMask);
	}
	if (ii.hbmColor) {
		DeleteObject(ii.hbmColor);
	}
	*w = *w > LC_POINTER_MAX ? LC_POINTER_MAX : *w;
	*h = *h > LC_POINTER_MAX ? LC_POINTER_MAX : *h;
	*cur = ci.hCursor;
	return !(*x >= bw || *y >= bh || *x + *w <= 0 || *y + *h <= 0);
}

// gpu_box is the part of comp the pointer at x, y, w by h covers.
static D3D11_BOX gpu_box(lc_gpucap *g, int x, int y, int w, int h) {
	D3D11_BOX b;
	b.left = x < 0 ? 0 : (UINT)x;
	b.top = y < 0 ? 0 : (UINT)y;
	b.right = x + w > g->cw ? (UINT)g->cw : (UINT)(x + w);
	b.bottom = y + h > g->ch ? (UINT)g->ch : (UINT)(y + h);
	b.front = 0;
	b.back = 1;
	return b;
}

// gpu_draw_pointer draws the pointer over comp: the square under it is read
// back, drawn on by GDI as any pointer is, and written back.
static void gpu_draw_pointer(lc_gpucap *g) {
	g->drawn = 0;
	HCURSOR cur;
	int x, y, w, h;
	if (!gpu_pointer(g->dmx, g->dmy, g->cw, g->ch, &cur, &x, &y, &w, &h)) {
		return;
	}
	if (!g->pstage) {
		D3D11_TEXTURE2D_DESC d;
		memset(&d, 0, sizeof d);
		d.Width = d.Height = LC_POINTER_MAX;
		d.MipLevels = d.ArraySize = 1;
		d.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
		d.SampleDesc.Count = 1;
		d.Usage = D3D11_USAGE_STAGING;
		d.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
		if (FAILED(ID3D11Device_CreateTexture2D(g->d->dev, &d, NULL, &g->pstage))) {
			return;
		}
	}
	if (!g->pbmp) {
		HDC sdc = GetDC(NULL);
		g->pdc = CreateCompatibleDC(sdc);
		BITMAPINFO bi;
		memset(&bi, 0, sizeof bi);
		bi.bmiHeader.biSize = sizeof bi.bmiHeader;
		bi.bmiHeader.biWidth = LC_POINTER_MAX;
		bi.bmiHeader.biHeight = -LC_POINTER_MAX;
		bi.bmiHeader.biPlanes = 1;
		bi.bmiHeader.biBitCount = 32;
		bi.bmiHeader.biCompression = BI_RGB;
		void *bits = NULL;
		g->pbmp = CreateDIBSection(sdc, &bi, DIB_RGB_COLORS, &bits, NULL, 0);
		ReleaseDC(NULL, sdc);
		if (!g->pbmp) {
			return;
		}
		g->pbits = bits;
		g->pold = SelectObject(g->pdc, g->pbmp);
	}
	D3D11_BOX b = gpu_box(g, x, y, w, h);
	int bw = (int)b.right - (int)b.left, bh = (int)b.bottom - (int)b.top;
	if (bw <= 0 || bh <= 0) {
		return;
	}
	ID3D11DeviceContext_CopySubresourceRegion(g->d->ctx, (ID3D11Resource *)g->pstage, 0, 0, 0, 0, (ID3D11Resource *)g->comp, 0, &b);
	D3D11_MAPPED_SUBRESOURCE m;
	if (FAILED(ID3D11DeviceContext_Map(g->d->ctx, (ID3D11Resource *)g->pstage, 0, D3D11_MAP_READ, 0, &m))) {
		return;
	}
	for (int row = 0; row < bh; row++) {
		memcpy(g->pbits + (size_t)row * LC_POINTER_MAX * 4, (uint8_t *)m.pData + (size_t)row * m.RowPitch, (size_t)bw * 4);
	}
	ID3D11DeviceContext_Unmap(g->d->ctx, (ID3D11Resource *)g->pstage, 0);
	DrawIconEx(g->pdc, x - (int)b.left, y - (int)b.top, cur, 0, 0, 0, NULL, DI_NORMAL);
	GdiFlush();
	ID3D11DeviceContext_UpdateSubresource(g->d->ctx, (ID3D11Resource *)g->comp, 0, &b, g->pbits, LC_POINTER_MAX * 4, 0);
	g->drawn = 1;
	g->cur = cur;
	g->px = x;
	g->py = y;
	g->pw = w;
	g->ph = h;
}

// gpu_pointer_moved reports whether the pointer is no longer as drawn.
static int gpu_pointer_moved(lc_gpucap *g) {
	HCURSOR cur;
	int x, y, w, h;
	int shows = gpu_pointer(g->dmx, g->dmy, g->cw, g->ch, &cur, &x, &y, &w, &h);
	if (!shows || !g->drawn) {
		return shows != g->drawn;
	}
	return cur != g->cur || x != g->px || y != g->py;
}

int lc_window_uncovered(uintptr_t hwnd, int sx, int sy, int w, int h);

int lc_gpucap_grab(lc_gpucap *g, int *w, int *h, int *changed, int *gone) {
	*gone = 0;
	*changed = 1;
	// Where the shared picture is, and on which screen.
	int mx = g->x, my = g->y, sx = 0, sy = 0, sw = 0, sh = 0;
	HANDLE dpi = lc_dpi_aware();
	if (g->hwnd) {
		HWND hwnd = (HWND)g->hwnd;
		if (!IsWindow(hwnd)) {
			*gone = 1;
			lc_dpi_restore(dpi);
			return 0;
		}
		RECT r;
		if (IsIconic(hwnd) || !GetClientRect(hwnd, &r) || r.right <= 0 || r.bottom <= 0) {
			lc_dpi_restore(dpi);
			return 0;
		}
		POINT so = {0, 0};
		ClientToScreen(hwnd, &so);
		MONITORINFO mi;
		memset(&mi, 0, sizeof mi);
		mi.cbSize = sizeof mi;
		if (!GetMonitorInfoW(MonitorFromWindow(hwnd, MONITOR_DEFAULTTONEAREST), &mi)) {
			lc_dpi_restore(dpi);
			return -2;
		}
		RECT m = mi.rcMonitor;
		// A window partly covered, or across two screens, is captured as it
		// draws itself, by the processor, this time.
		if (so.x < m.left || so.y < m.top || so.x + r.right > m.right || so.y + r.bottom > m.bottom ||
		    !lc_window_uncovered(g->hwnd, so.x, so.y, r.right, r.bottom)) {
			lc_dpi_restore(dpi);
			return -2;
		}
		mx = m.left;
		my = m.top;
		sx = so.x - m.left;
		sy = so.y - m.top;
		sw = r.right;
		sh = r.bottom;
	}
	if (g->d && (g->dmx != mx || g->dmy != my)) {
		lc_dupl_close(g->d);
		g->d = NULL;
	}
	if (!g->d) {
		g->d = lc_dupl_open(mx, my);
		g->d->gpu = 1;
		g->dmx = mx;
		g->dmy = my;
	}
	int r = poll(g->d);
	lc_dpi_restore(dpi);
	if (r < 0) {
		return -1;
	}
	if (r == 0 || !g->d->gframe) {
		return 0;
	}
	if (g->devgen != g->d->gen) {
		// A new device, as after a change of resolution: everything anew.
		gpu_release(g);
		g->devgen = g->d->gen;
		g->gen++;
	}
	int fw = g->d->fw, fh = g->d->fh;
	if (!g->hwnd) {
		sw = fw;
		sh = fh;
	}
	if (sx + sw > fw || sy + sh > fh) {
		return -2;
	}
	int fresh = 0;
	if (!g->comp || g->cw != fw || g->ch != fh) {
		LC_REL(g->comp, ID3D11Texture2D_Release);
		LC_REL(g->preview, ID3D11Texture2D_Release);
		gpu_release_vp(g);
		D3D11_TEXTURE2D_DESC d;
		memset(&d, 0, sizeof d);
		d.Width = (UINT)fw;
		d.Height = (UINT)fh;
		d.MipLevels = d.ArraySize = 1;
		d.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
		d.SampleDesc.Count = 1;
		d.Usage = D3D11_USAGE_DEFAULT;
		d.BindFlags = D3D11_BIND_SHADER_RESOURCE | D3D11_BIND_RENDER_TARGET;
		if (FAILED(ID3D11Device_CreateTexture2D(g->d->dev, &d, NULL, &g->comp))) {
			return -1;
		}
		g->cw = fw;
		g->ch = fh;
		fresh = 1;
	}
	int moved = sx != g->sx || sy != g->sy || sw != g->sw || sh != g->sh;
	g->sx = sx;
	g->sy = sy;
	g->sw = sw;
	g->sh = sh;
	if (fresh || g->seq != g->d->seq) {
		ID3D11DeviceContext_CopyResource(g->d->ctx, (ID3D11Resource *)g->comp, (ID3D11Resource *)g->d->gframe);
		g->seq = g->d->seq;
		g->drawn = 0;
		if (g->cursor) {
			gpu_draw_pointer(g);
		}
	} else if (g->cursor && gpu_pointer_moved(g)) {
		if (g->drawn) {
			D3D11_BOX b = gpu_box(g, g->px, g->py, g->pw, g->ph);
			if (b.right > b.left && b.bottom > b.top) {
				ID3D11DeviceContext_CopySubresourceRegion(g->d->ctx, (ID3D11Resource *)g->comp, 0, b.left, b.top, 0, (ID3D11Resource *)g->d->gframe, 0, &b);
			}
		}
		gpu_draw_pointer(g);
	} else if (!moved) {
		*changed = 0;
	}
	*w = sw;
	*h = sh;
	return 1;
}

// gpu_vp makes the video processor from comp to ow by oh NV12 textures.
static int gpu_vp(lc_gpucap *g, int ow, int oh) {
	if (g->vp && g->vw == g->cw && g->vh == g->ch && g->ow == ow && g->oh == oh && g->inview) {
		return 1;
	}
	gpu_release_vp(g);
	if (!g->vdev) {
		if (FAILED(ID3D11Device_QueryInterface(g->d->dev, &lc_IID_ID3D11VideoDevice, (void **)&g->vdev)) ||
		    FAILED(ID3D11DeviceContext_QueryInterface(g->d->ctx, &lc_IID_ID3D11VideoContext, (void **)&g->vctx))) {
			return 0;
		}
	}
	D3D11_VIDEO_PROCESSOR_CONTENT_DESC cd;
	memset(&cd, 0, sizeof cd);
	cd.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
	cd.InputWidth = (UINT)g->cw;
	cd.InputHeight = (UINT)g->ch;
	cd.OutputWidth = (UINT)ow;
	cd.OutputHeight = (UINT)oh;
	cd.InputFrameRate.Numerator = cd.OutputFrameRate.Numerator = 60;
	cd.InputFrameRate.Denominator = cd.OutputFrameRate.Denominator = 1;
	cd.Usage = D3D11_VIDEO_USAGE_OPTIMAL_SPEED;
	if (FAILED(ID3D11VideoDevice_CreateVideoProcessorEnumerator(g->vdev, &cd, &g->venum)) ||
	    FAILED(ID3D11VideoDevice_CreateVideoProcessor(g->vdev, g->venum, 0, &g->vp))) {
		return 0;
	}
	D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC iv;
	memset(&iv, 0, sizeof iv);
	iv.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
	if (FAILED(ID3D11VideoDevice_CreateVideoProcessorInputView(g->vdev, (ID3D11Resource *)g->comp, g->venum, &iv, &g->inview))) {
		return 0;
	}
	D3D11_TEXTURE2D_DESC d;
	memset(&d, 0, sizeof d);
	d.Width = (UINT)ow;
	d.Height = (UINT)oh;
	d.MipLevels = d.ArraySize = 1;
	d.Format = DXGI_FORMAT_NV12;
	d.SampleDesc.Count = 1;
	d.Usage = D3D11_USAGE_DEFAULT;
	d.BindFlags = D3D11_BIND_RENDER_TARGET;
	D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC ov;
	memset(&ov, 0, sizeof ov);
	ov.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
	for (int i = 0; i < LC_GPU_SLOTS; i++) {
		if (FAILED(ID3D11Device_CreateTexture2D(g->d->dev, &d, NULL, &g->slot[i])) ||
		    FAILED(ID3D11VideoDevice_CreateVideoProcessorOutputView(g->vdev, (ID3D11Resource *)g->slot[i], g->venum, &ov, &g->outview[i]))) {
			return 0;
		}
	}
	// Full-range RGB in, BT.601 limited-range YUV out: as the processor's
	// own conversion, so that a stream looks the same either way.
	D3D11_VIDEO_PROCESSOR_COLOR_SPACE in, out;
	memset(&in, 0, sizeof in);
	memset(&out, 0, sizeof out);
	in.RGB_Range = 0;
	out.YCbCr_Matrix = 0;
	out.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_16_235;
	ID3D11VideoContext_VideoProcessorSetStreamColorSpace(g->vctx, g->vp, 0, &in);
	ID3D11VideoContext_VideoProcessorSetOutputColorSpace(g->vctx, g->vp, &out);
	ID3D11VideoContext_VideoProcessorSetStreamAutoProcessingMode(g->vctx, g->vp, 0, FALSE);
	ID3D11VideoContext_VideoProcessorSetStreamFrameFormat(g->vctx, g->vp, 0, D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE);
	g->vw = g->cw;
	g->vh = g->ch;
	g->ow = ow;
	g->oh = oh;
	return 1;
}

int lc_gpucap_convert(lc_gpucap *g, int ow, int oh, int *slot) {
	if (!g->comp || !gpu_vp(g, ow, oh)) {
		gpu_release_vp(g);
		return -1;
	}
	RECT src = {g->sx, g->sy, g->sx + g->sw, g->sy + g->sh};
	RECT dst = {0, 0, ow, oh};
	ID3D11VideoContext_VideoProcessorSetStreamSourceRect(g->vctx, g->vp, 0, TRUE, &src);
	ID3D11VideoContext_VideoProcessorSetStreamDestRect(g->vctx, g->vp, 0, TRUE, &dst);
	ID3D11VideoContext_VideoProcessorSetOutputTargetRect(g->vctx, g->vp, TRUE, &dst);
	D3D11_VIDEO_PROCESSOR_STREAM st;
	memset(&st, 0, sizeof st);
	st.Enable = TRUE;
	st.pInputSurface = g->inview;
	int i = g->next;
	if (FAILED(ID3D11VideoContext_VideoProcessorBlt(g->vctx, g->vp, g->outview[i], 0, 1, &st))) {
		return -1;
	}
	// On its way to the graphics card now, for the encoder to find it done.
	ID3D11DeviceContext_Flush(g->d->ctx);
	g->next = (i + 1) % LC_GPU_SLOTS;
	*slot = i;
	return 0;
}

void *lc_gpucap_device(lc_gpucap *g) {
	if (!g->d || !g->d->dev) {
		return NULL;
	}
	ID3D11Device_AddRef(g->d->dev);
	return g->d->dev;
}

void lc_com_addref(void *p) {
	if (p) {
		IUnknown *u = p;
		u->lpVtbl->AddRef(u);
	}
}

void lc_com_release(void *p) {
	if (p) {
		IUnknown *u = p;
		u->lpVtbl->Release(u);
	}
}

int lc_gpucap_gen(lc_gpucap *g) {
	return g->gen;
}

void *lc_gpucap_texture(lc_gpucap *g, int slot) {
	if (slot < 0 || slot >= LC_GPU_SLOTS || !g->slot[slot]) {
		return NULL;
	}
	ID3D11Texture2D_AddRef(g->slot[slot]);
	return g->slot[slot];
}

int lc_gpucap_preview(lc_gpucap *g, uint8_t *rgba, int w, int h) {
	if (!g->comp) {
		return -1;
	}
	if (!g->preview) {
		D3D11_TEXTURE2D_DESC d;
		ID3D11Texture2D_GetDesc(g->comp, &d);
		d.Usage = D3D11_USAGE_STAGING;
		d.BindFlags = 0;
		d.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
		d.MiscFlags = 0;
		if (FAILED(ID3D11Device_CreateTexture2D(g->d->dev, &d, NULL, &g->preview))) {
			return -1;
		}
	}
	ID3D11DeviceContext_CopyResource(g->d->ctx, (ID3D11Resource *)g->preview, (ID3D11Resource *)g->comp);
	D3D11_MAPPED_SUBRESOURCE m;
	if (FAILED(ID3D11DeviceContext_Map(g->d->ctx, (ID3D11Resource *)g->preview, 0, D3D11_MAP_READ, 0, &m))) {
		return -1;
	}
	const uint8_t *p = (const uint8_t *)m.pData + (size_t)g->sy * m.RowPitch + (size_t)g->sx * 4;
	lc_bgra_to_rgba(p, g->sw, g->sh, (int)m.RowPitch, rgba, w, h);
	ID3D11DeviceContext_Unmap(g->d->ctx, (ID3D11Resource *)g->preview, 0);
	return 0;
}

int lc_texture_read_nv12(void *texture, uint8_t *dst, int w, int h) {
	ID3D11Texture2D *tex = texture;
	ID3D11Device *dev = NULL;
	ID3D11Texture2D_GetDevice(tex, &dev);
	ID3D11DeviceContext *ctx = NULL;
	ID3D11Device_GetImmediateContext(dev, &ctx);
	D3D11_TEXTURE2D_DESC d;
	ID3D11Texture2D_GetDesc(tex, &d);
	d.Usage = D3D11_USAGE_STAGING;
	d.BindFlags = 0;
	d.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
	d.MiscFlags = 0;
	ID3D11Texture2D *st = NULL;
	int r = -1;
	if (SUCCEEDED(ID3D11Device_CreateTexture2D(dev, &d, NULL, &st))) {
		ID3D11DeviceContext_CopyResource(ctx, (ID3D11Resource *)st, (ID3D11Resource *)tex);
		D3D11_MAPPED_SUBRESOURCE m;
		if (SUCCEEDED(ID3D11DeviceContext_Map(ctx, (ID3D11Resource *)st, 0, D3D11_MAP_READ, 0, &m))) {
			for (int y = 0; y < h; y++) {
				memcpy(dst + (size_t)y * w, (uint8_t *)m.pData + (size_t)y * m.RowPitch, (size_t)w);
			}
			const uint8_t *uv = (uint8_t *)m.pData + (size_t)m.RowPitch * d.Height;
			for (int y = 0; y < h / 2; y++) {
				memcpy(dst + (size_t)w * h + (size_t)y * w, uv + (size_t)y * m.RowPitch, (size_t)w);
			}
			ID3D11DeviceContext_Unmap(ctx, (ID3D11Resource *)st, 0);
			r = 0;
		}
		ID3D11Texture2D_Release(st);
	}
	ID3D11DeviceContext_Release(ctx);
	ID3D11Device_Release(dev);
	return r;
}