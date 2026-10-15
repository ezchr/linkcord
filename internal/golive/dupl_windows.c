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
	// worked reports whether the screen was ever duplicated, and retry when
	// to try again after it stopped, as it does when a game changes the
	// resolution or Windows shows its secure desktop.
	int worked;
	DWORD retry;
};

static void release(lc_dupl *d) {
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
			hr = create_device((IDXGIAdapter *)a, D3D_DRIVER_TYPE_UNKNOWN, NULL, 0, NULL, 0, D3D11_SDK_VERSION, &d->dev, NULL, &d->ctx);
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

int lc_dupl_grab(lc_dupl *d, const uint8_t **pix, int *w, int *h) {
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
	if (!d->have) {
		return 0;
	}
	*pix = d->frame;
	*w = d->fw;
	*h = d->fh;
	return 1;
}

void lc_dupl_close(lc_dupl *d) {
	if (!d) {
		return;
	}
	release(d);
	free(d->frame);
	free(d);
}
