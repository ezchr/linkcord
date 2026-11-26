//go:build windows && cgo

// Decoding pictures with the Windows Imaging Component, which ships with
// every Windows since Vista. Pictures are shrunk while they decode to the
// size they are shown at: a JPEG is decoded at an eighth, a quarter or half
// of its size by its own DCT where that is still at least as big as needed,
// so a photo is never decoded whole to become a thumbnail. WIC is created
// through COM at run time, so nothing here needs a DLL Windows 7 lacks;
// where a codec is missing (WebP before Windows 10 1809), opening fails and
// the Go decoders are used instead.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <objbase.h>
#include <wincodec.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "wic.h"

#define LC_WGUID(name, l, w1, w2, b1, b2, b3, b4, b5, b6, b7, b8) \
	static const GUID name = {l, w1, w2, {b1, b2, b3, b4, b5, b6, b7, b8}}
// The first imaging factory, the one Windows 7 has.
LC_WGUID(lc_CLSID_WICImagingFactory, 0xcacaf262, 0x9370, 0x4615, 0xa1, 0x3b, 0x9f, 0x55, 0x39, 0xda, 0x4c, 0x0a);
LC_WGUID(lc_IID_IWICImagingFactory, 0xec5ec8a9, 0xc395, 0x4314, 0x9c, 0x77, 0x54, 0xd7, 0xa9, 0x35, 0xff, 0x70);
LC_WGUID(lc_IID_IWICBitmapSourceTransform, 0x3b16811b, 0x6a43, 0x4ec9, 0xb7, 0x13, 0x3d, 0x5a, 0x0c, 0x13, 0xb9, 0x40);
LC_WGUID(lc_PBGRA, 0x6fddc324, 0x4e03, 0x4bfe, 0xb1, 0x85, 0x3d, 0x77, 0x76, 0x8d, 0xc9, 0x10);
LC_WGUID(lc_BGR24, 0x6fddc324, 0x4e03, 0x4bfe, 0xb1, 0x85, 0x3d, 0x77, 0x76, 0x8d, 0xc9, 0x0c);
LC_WGUID(lc_ContainerGif, 0x1f8a5601, 0x7d4d, 0x4cbd, 0x9c, 0x82, 0x1b, 0xc8, 0xd4, 0xee, 0xb9, 0xa5);
LC_WGUID(lc_ContainerJpeg, 0x19e4a5aa, 0x5662, 0x4fc5, 0xa0, 0xc0, 0x17, 0x58, 0x02, 0x8e, 0x10, 0x57);
LC_WGUID(lc_ContainerIco, 0xa3a860c4, 0x338f, 0x4c17, 0x91, 0x9a, 0xfb, 0xa4, 0xb5, 0x62, 0x8f, 0x21);

// IWICBitmapSourceTransform, which MinGW's wincodec.h leaves out.
typedef struct lc_SourceTransform lc_SourceTransform;
typedef struct {
	HRESULT(STDMETHODCALLTYPE *QueryInterface)(lc_SourceTransform *, REFIID, void **);
	ULONG(STDMETHODCALLTYPE *AddRef)(lc_SourceTransform *);
	ULONG(STDMETHODCALLTYPE *Release)(lc_SourceTransform *);
	HRESULT(STDMETHODCALLTYPE *CopyPixels)(lc_SourceTransform *, const WICRect *, UINT, UINT, WICPixelFormatGUID *, WICBitmapTransformOptions, UINT, UINT, BYTE *);
	HRESULT(STDMETHODCALLTYPE *GetClosestSize)(lc_SourceTransform *, UINT *, UINT *);
	HRESULT(STDMETHODCALLTYPE *GetClosestPixelFormat)(lc_SourceTransform *, WICPixelFormatGUID *);
	HRESULT(STDMETHODCALLTYPE *DoesSupportTransform)(lc_SourceTransform *, WICBitmapTransformOptions, BOOL *);
} lc_SourceTransformVtbl;
struct lc_SourceTransform {
	const lc_SourceTransformVtbl *lpVtbl;
};

#define WIC_E_LIMIT ((int)0x80070216) // ERROR_ARITHMETIC_OVERFLOW
#define WIC_E_ARG ((int)E_INVALIDARG)

typedef struct {
	IWICImagingFactory *f;
	IWICStream *s;
	IWICBitmapDecoder *d;
	GUID container;
} wic_ctx;

// One factory serves every call. Joining and leaving COM's apartment at
// each call took 5 ms, as the last thread to leave unloads WIC: a thread of
// its own stays in the multithreaded apartment, which keeps it, and the
// threads Go calls on use it implicitly. WIC's objects are free threaded.
static INIT_ONCE wic_once = INIT_ONCE_STATIC_INIT;
static IWICImagingFactory *wic_factory;
static HANDLE wic_ready;

static DWORD WINAPI wic_keeper(LPVOID arg) {
	(void)arg;
	if (SUCCEEDED(CoInitializeEx(NULL, COINIT_MULTITHREADED))) {
		IWICImagingFactory *f = NULL;
		if (SUCCEEDED(CoCreateInstance(&lc_CLSID_WICImagingFactory, NULL, CLSCTX_INPROC_SERVER, &lc_IID_IWICImagingFactory, (void **)&f))) {
			wic_factory = f;
		}
	}
	SetEvent(wic_ready);
	if (wic_factory) {
		for (;;) Sleep(INFINITE);
	}
	return 0;
}

static BOOL CALLBACK wic_start(PINIT_ONCE once, PVOID param, PVOID *ctx) {
	(void)once;
	(void)param;
	(void)ctx;
	wic_ready = CreateEventW(NULL, TRUE, FALSE, NULL);
	if (!wic_ready) return TRUE;
	HANDLE t = CreateThread(NULL, 64 << 10, wic_keeper, NULL, STACK_SIZE_PARAM_IS_A_RESERVATION, NULL);
	if (t) {
		WaitForSingleObject(wic_ready, INFINITE);
		CloseHandle(t);
	}
	CloseHandle(wic_ready);
	return TRUE;
}

static void wic_close(wic_ctx *c) {
	if (c->d) IWICBitmapDecoder_Release(c->d);
	if (c->s) IWICStream_Release(c->s);
	memset(c, 0, sizeof *c);
}

// wic_open reads the header of the picture in data.
static HRESULT wic_open(wic_ctx *c, const void *data, size_t len) {
	memset(c, 0, sizeof *c);
	if (!data || len == 0 || len > 0x7fffffff) return WIC_E_ARG;
	InitOnceExecuteOnce(&wic_once, wic_start, NULL, NULL);
	if (!wic_factory) return REGDB_E_CLASSNOTREG;
	c->f = wic_factory;
	HRESULT hr = IWICImagingFactory_CreateStream(c->f, &c->s);
	if (FAILED(hr)) goto fail;
	hr = IWICStream_InitializeFromMemory(c->s, (BYTE *)data, (DWORD)len);
	if (FAILED(hr)) goto fail;
	hr = IWICImagingFactory_CreateDecoderFromStream(c->f, (IStream *)c->s, NULL, WICDecodeMetadataCacheOnDemand, &c->d);
	if (FAILED(hr)) goto fail;
	hr = IWICBitmapDecoder_GetContainerFormat(c->d, &c->container);
	if (FAILED(hr)) goto fail;
	return S_OK;
fail:
	wic_close(c);
	return hr;
}

// meta_uint reads a whole number from a metadata query reader, or def.
static unsigned meta_uint(IWICMetadataQueryReader *r, const wchar_t *name, unsigned def) {
	if (!r) return def;
	PROPVARIANT v;
	PropVariantInit(&v);
	unsigned out = def;
	if (SUCCEEDED(IWICMetadataQueryReader_GetMetadataByName(r, name, &v))) {
		switch (v.vt) {
		case VT_UI1: out = v.bVal; break;
		case VT_UI2: out = v.uiVal; break;
		case VT_UI4: out = v.ulVal; break;
		case VT_I4: out = v.lVal >= 0 ? (unsigned)v.lVal : def; break;
		}
	}
	PropVariantClear(&v);
	return out;
}

// pick_frame is the frame a still is made of: the first, or the largest of
// an icon.
static HRESULT pick_frame(wic_ctx *c, IWICBitmapFrameDecode **out, UINT *w, UINT *h) {
	UINT n = 0;
	HRESULT hr = IWICBitmapDecoder_GetFrameCount(c->d, &n);
	if (FAILED(hr)) return hr;
	if (n == 0) return WINCODEC_ERR_FRAMEMISSING;
	UINT best = 0;
	if (IsEqualGUID(&c->container, &lc_ContainerIco) && n > 1) {
		unsigned long long area = 0;
		for (UINT i = 0; i < n && i < 64; i++) {
			IWICBitmapFrameDecode *fr = NULL;
			UINT fw = 0, fh = 0;
			if (SUCCEEDED(IWICBitmapDecoder_GetFrame(c->d, i, &fr)) && SUCCEEDED(IWICBitmapFrameDecode_GetSize(fr, &fw, &fh))) {
				if ((unsigned long long)fw * fh > area) {
					area = (unsigned long long)fw * fh;
					best = i;
				}
			}
			if (fr) IWICBitmapFrameDecode_Release(fr);
		}
	}
	hr = IWICBitmapDecoder_GetFrame(c->d, best, out);
	if (FAILED(hr)) return hr;
	hr = IWICBitmapFrameDecode_GetSize(*out, w, h);
	if (FAILED(hr)) {
		IWICBitmapFrameDecode_Release(*out);
		*out = NULL;
	}
	return hr;
}

int wic_info(const void *data, size_t len, wic_info_t *info) {
	memset(info, 0, sizeof *info);
	wic_ctx c;
	HRESULT hr = wic_open(&c, data, len);
	if (FAILED(hr)) return hr;
	IWICBitmapFrameDecode *fr = NULL;
	UINT w = 0, h = 0, n = 0;
	hr = pick_frame(&c, &fr, &w, &h);
	if (SUCCEEDED(hr)) hr = IWICBitmapDecoder_GetFrameCount(c.d, &n);
	if (SUCCEEDED(hr)) {
		info->w = (int)w;
		info->h = (int)h;
		info->frames = (int)n;
		info->jpeg = IsEqualGUID(&c.container, &lc_ContainerJpeg);
		info->gif = IsEqualGUID(&c.container, &lc_ContainerGif);
		info->canvas_w = (int)w;
		info->canvas_h = (int)h;
		if (info->gif) {
			// An animated GIF's frames are drawn on its logical screen.
			IWICMetadataQueryReader *r = NULL;
			if (SUCCEEDED(IWICBitmapDecoder_GetMetadataQueryReader(c.d, &r))) {
				info->canvas_w = (int)meta_uint(r, L"/logscrdesc/Width", w);
				info->canvas_h = (int)meta_uint(r, L"/logscrdesc/Height", h);
				IWICMetadataQueryReader_Release(r);
			}
		}
	}
	if (fr) IWICBitmapFrameDecode_Release(fr);
	wic_close(&c);
	return hr;
}

// swap_rb turns premultiplied BGRA into the premultiplied RGBA Go's
// image.RGBA and Gio use.
static void swap_rb(uint8_t *p, size_t n) {
	for (size_t i = 0; i < n; i += 4) {
		uint8_t t = p[i];
		p[i] = p[i + 2];
		p[i + 2] = t;
	}
}

// jpeg_reduced decodes a JPEG frame at the smallest of its DCT scales
// (1/8, 1/4, 1/2) that is still at least tw×th, into a new bitmap. It
// returns NULL where the decoder can't, and the full decode is used.
static IWICBitmapSource *jpeg_reduced(wic_ctx *c, IWICBitmapFrameDecode *fr, UINT w, UINT h, UINT tw, UINT th) {
	UINT sw = 0, sh = 0;
	for (UINT f = 8; f >= 2; f /= 2) {
		UINT cw = (w + f - 1) / f, ch = (h + f - 1) / f;
		if (cw >= tw && ch >= th) {
			sw = cw;
			sh = ch;
			break;
		}
	}
	if (sw == 0) return NULL;
	lc_SourceTransform *t = NULL;
	if (FAILED(IWICBitmapFrameDecode_QueryInterface(fr, &lc_IID_IWICBitmapSourceTransform, (void **)&t))) return NULL;
	IWICBitmap *bmp = NULL;
	IWICBitmapLock *lock = NULL;
	IWICBitmapSource *out = NULL;
	UINT cw = sw, ch = sh;
	WICPixelFormatGUID fmt = lc_BGR24;
	if (FAILED(t->lpVtbl->GetClosestSize(t, &cw, &ch)) || cw < tw || ch < th || cw > w || ch > h) goto done;
	if (FAILED(t->lpVtbl->GetClosestPixelFormat(t, &fmt))) goto done;
	if (FAILED(IWICImagingFactory_CreateBitmap(c->f, cw, ch, &fmt, WICBitmapCacheOnLoad, &bmp))) goto done;
	WICRect all = {0, 0, (INT)cw, (INT)ch};
	if (FAILED(IWICBitmap_Lock(bmp, &all, WICBitmapLockWrite, &lock))) goto done;
	UINT stride = 0, size = 0;
	BYTE *p = NULL;
	if (FAILED(IWICBitmapLock_GetStride(lock, &stride)) || FAILED(IWICBitmapLock_GetDataPointer(lock, &size, &p))) goto done;
	if (FAILED(t->lpVtbl->CopyPixels(t, NULL, cw, ch, &fmt, WICBitmapTransformRotate0, stride, size, p))) goto done;
	IWICBitmapLock_Release(lock);
	lock = NULL;
	out = (IWICBitmapSource *)bmp;
	bmp = NULL;
done:
	if (lock) IWICBitmapLock_Release(lock);
	if (bmp) IWICBitmap_Release(bmp);
	t->lpVtbl->Release(t);
	return out;
}

// render converts src to premultiplied BGRA, shrinks it to tw×th with
// Fant's filter, which averages every source pixel, and copies it to dst.
static HRESULT render(wic_ctx *c, IWICBitmapSource *src, UINT tw, UINT th, uint8_t *dst) {
	IWICFormatConverter *conv = NULL;
	IWICBitmapScaler *sc = NULL;
	UINT sw = 0, sh = 0;
	HRESULT hr = IWICBitmapSource_GetSize(src, &sw, &sh);
	if (FAILED(hr)) return hr;
	hr = IWICImagingFactory_CreateFormatConverter(c->f, &conv);
	if (FAILED(hr)) return hr;
	hr = IWICFormatConverter_Initialize(conv, src, &lc_PBGRA, WICBitmapDitherTypeNone, NULL, 0.0, WICBitmapPaletteTypeCustom);
	IWICBitmapSource *last = (IWICBitmapSource *)conv;
	if (SUCCEEDED(hr) && (sw != tw || sh != th)) {
		hr = IWICImagingFactory_CreateBitmapScaler(c->f, &sc);
		if (SUCCEEDED(hr)) hr = IWICBitmapScaler_Initialize(sc, last, tw, th, WICBitmapInterpolationModeFant);
		last = (IWICBitmapSource *)sc;
	}
	if (SUCCEEDED(hr)) hr = IWICBitmapSource_CopyPixels(last, NULL, tw * 4, tw * th * 4, dst);
	if (sc) IWICBitmapScaler_Release(sc);
	IWICFormatConverter_Release(conv);
	return hr;
}

static int bad_size(int w, int h, int max_side, long long max_px) {
	return w <= 0 || h <= 0 || w > max_side || h > max_side || (long long)w * h > max_px;
}

int wic_still(const void *data, size_t len, int tw, int th, uint8_t *dst) {
	if (!dst || bad_size(tw, th, WIC_MAX_OUT_SIDE, WIC_MAX_OUT_PIXELS)) return WIC_E_ARG;
	wic_ctx c;
	HRESULT hr = wic_open(&c, data, len);
	if (FAILED(hr)) return hr;
	IWICBitmapFrameDecode *fr = NULL;
	IWICBitmapSource *src = NULL;
	UINT w = 0, h = 0;
	hr = pick_frame(&c, &fr, &w, &h);
	if (FAILED(hr)) goto done;
	if ((UINT)tw > w || (UINT)th > h) {
		hr = WIC_E_ARG; // never made larger than it is
		goto done;
	}
	if (IsEqualGUID(&c.container, &lc_ContainerJpeg) && ((UINT)tw * 2 <= w && (UINT)th * 2 <= h)) {
		src = jpeg_reduced(&c, fr, w, h, (UINT)tw, (UINT)th);
	}
	if (!src) {
		// Only the JPEG path above avoids a whole decode, so others are
		// bounded by their size.
		if (bad_size((int)w, (int)h, WIC_MAX_IN_SIDE, WIC_MAX_IN_PIXELS)) {
			hr = WIC_E_LIMIT;
			goto done;
		}
		IWICBitmapFrameDecode_AddRef(fr);
		src = (IWICBitmapSource *)fr;
	}
	hr = render(&c, src, (UINT)tw, (UINT)th, dst);
	if (SUCCEEDED(hr)) swap_rb(dst, (size_t)tw * th * 4);
done:
	if (src) IWICBitmapSource_Release(src);
	if (fr) IWICBitmapFrameDecode_Release(fr);
	wic_close(&c);
	return hr;
}

int wic_gif(const void *data, size_t len, int cw, int ch, int tw, int th, int step, int keep, uint8_t *dst, int *delays, int ndelays) {
	if (!dst || !delays || step < 1 || keep < 1 || bad_size(cw, ch, WIC_MAX_IN_SIDE, WIC_MAX_CANVAS_PIXELS) ||
	    bad_size(tw, th, WIC_MAX_OUT_SIDE, WIC_MAX_OUT_PIXELS) || tw > cw || th > ch) {
		return WIC_E_ARG;
	}
	wic_ctx c;
	HRESULT hr = wic_open(&c, data, len);
	if (FAILED(hr)) return hr;
	UINT n = 0;
	uint8_t *canvas = NULL, *before = NULL, *pix = NULL;
	size_t pixcap = 0;
	IWICBitmap *bmp = NULL;
	hr = IWICBitmapDecoder_GetFrameCount(c.d, &n);
	if (FAILED(hr)) goto done;
	if (!IsEqualGUID(&c.container, &lc_ContainerGif) || (int)n != ndelays) {
		hr = WIC_E_ARG;
		goto done;
	}
	size_t cbytes = (size_t)cw * ch * 4, obytes = (size_t)tw * th * 4;
	canvas = calloc(1, cbytes);
	if (!canvas) {
		hr = E_OUTOFMEMORY;
		goto done;
	}
	int kept = 0;
	for (UINT i = 0; i < n; i++) {
		IWICBitmapFrameDecode *fr = NULL;
		IWICMetadataQueryReader *r = NULL;
		IWICFormatConverter *conv = NULL;
		hr = IWICBitmapDecoder_GetFrame(c.d, i, &fr);
		if (FAILED(hr)) goto done;
		IWICBitmapFrameDecode_GetMetadataQueryReader(fr, &r);
		unsigned left = meta_uint(r, L"/imgdesc/Left", 0), top = meta_uint(r, L"/imgdesc/Top", 0);
		unsigned disposal = meta_uint(r, L"/grctlext/Disposal", 0);
		delays[i] = (int)meta_uint(r, L"/grctlext/Delay", 0);
		if (r) IWICMetadataQueryReader_Release(r);
		UINT fw = 0, fh = 0;
		hr = IWICBitmapFrameDecode_GetSize(fr, &fw, &fh);
		if (SUCCEEDED(hr) && bad_size((int)fw, (int)fh, WIC_MAX_IN_SIDE, WIC_MAX_CANVAS_PIXELS)) hr = WIC_E_LIMIT;
		size_t need = (size_t)fw * fh * 4;
		if (SUCCEEDED(hr) && need > pixcap) {
			free(pix);
			pix = malloc(need);
			pixcap = pix ? need : 0;
			if (!pix) hr = E_OUTOFMEMORY;
		}
		if (SUCCEEDED(hr)) hr = IWICImagingFactory_CreateFormatConverter(c.f, &conv);
		if (SUCCEEDED(hr)) hr = IWICFormatConverter_Initialize(conv, (IWICBitmapSource *)fr, &lc_PBGRA, WICBitmapDitherTypeNone, NULL, 0.0, WICBitmapPaletteTypeCustom);
		if (SUCCEEDED(hr)) hr = IWICFormatConverter_CopyPixels(conv, NULL, fw * 4, (UINT)need, pix);
		if (conv) IWICFormatConverter_Release(conv);
		IWICBitmapFrameDecode_Release(fr);
		if (FAILED(hr)) goto done;
		if (disposal == 3) {
			if (!before) before = malloc(cbytes);
			if (!before) {
				hr = E_OUTOFMEMORY;
				goto done;
			}
			memcpy(before, canvas, cbytes);
		}
		// The frame's rectangle, clipped to the canvas. GIF pixels are
		// either opaque or transparent, so drawing over is a copy of the
		// opaque ones.
		UINT x0 = left, y0 = top;
		UINT x1 = x0 < (UINT)cw ? (x0 + fw < (UINT)cw ? x0 + fw : (UINT)cw) : x0;
		UINT y1 = y0 < (UINT)ch ? (y0 + fh < (UINT)ch ? y0 + fh : (UINT)ch) : y0;
		for (UINT y = y0; y < y1; y++) {
			const uint8_t *s = pix + (size_t)(y - y0) * fw * 4;
			uint8_t *d = canvas + ((size_t)y * cw + x0) * 4;
			for (UINT x = x0; x < x1; x++, s += 4, d += 4) {
				if (s[3]) memcpy(d, s, 4);
			}
		}
		if ((int)i % step == 0 && kept < keep) {
			uint8_t *out = dst + (size_t)kept * obytes;
			if (cw != tw || ch != th) {
				// WIC copies the canvas into a bitmap of its own to shrink.
				hr = IWICImagingFactory_CreateBitmapFromMemory(c.f, cw, ch, &lc_PBGRA, cw * 4, (UINT)cbytes, canvas, &bmp);
				if (SUCCEEDED(hr)) hr = render(&c, (IWICBitmapSource *)bmp, (UINT)tw, (UINT)th, out);
				if (bmp) IWICBitmap_Release(bmp);
				bmp = NULL;
				if (FAILED(hr)) goto done;
			} else {
				memcpy(out, canvas, obytes);
			}
			swap_rb(out, obytes);
			kept++;
		}
		if (disposal == 2) {
			for (UINT y = y0; y < y1; y++) memset(canvas + ((size_t)y * cw + x0) * 4, 0, (size_t)(x1 - x0) * 4);
		} else if (disposal == 3) {
			memcpy(canvas, before, cbytes);
		}
	}
	hr = kept;
done:
	if (bmp) IWICBitmap_Release(bmp);
	free(canvas);
	free(before);
	free(pix);
	wic_close(&c);
	return hr;
}
