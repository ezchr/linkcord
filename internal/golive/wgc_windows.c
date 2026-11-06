//go:build windows && !no_voice

// Capturing a screen with Windows.Graphics.Capture, which Windows 10 2004 and
// later have. Unlike desktop duplication it is handed every picture the
// compositor makes, video played in full screen too. It is WinRT, used here
// through its vtables and loaded when needed, so that Linkcord starts on
// Windows that lack it, where desktop duplication goes on as before.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <stdlib.h>

#include "native.h"

static const GUID lc_IID_ItemInterop = {0x3628e81b, 0x3cac, 0x4c60, {0xb7, 0xf4, 0x23, 0xce, 0x0e, 0x0c, 0x33, 0x56}};
static const GUID lc_IID_Item = {0x79c3f95b, 0x31f7, 0x4ec2, {0xa4, 0x64, 0x63, 0x2e, 0xf5, 0xd3, 0x07, 0x60}};
static const GUID lc_IID_PoolStatics2 = {0x589b103f, 0x6bbc, 0x5df5, {0xa9, 0x91, 0x02, 0xe2, 0x8b, 0x3b, 0x66, 0xd5}};
static const GUID lc_IID_Session2 = {0x2c39ae40, 0x7d2e, 0x5044, {0x80, 0x4e, 0x8b, 0x67, 0x99, 0xd4, 0xcf, 0x9e}};
static const GUID lc_IID_Session3 = {0xf2cdd966, 0x22ae, 0x5ea1, {0x95, 0x96, 0x3a, 0x28, 0x93, 0x44, 0xc3, 0xbe}};
static const GUID lc_IID_Access = {0xa9b3d012, 0x3df2, 0x4ee3, {0xb8, 0xd1, 0x86, 0x95, 0xf4, 0x57, 0xd3, 0xc1}};
static const GUID lc_IID_Closable = {0x30d5a829, 0x7fa4, 0x4026, {0x83, 0xbb, 0xd7, 0x5b, 0xae, 0x4e, 0xa9, 0x9e}};
static const GUID lc_IID_DXGIDevice = {0x54ec77fa, 0x1377, 0x44e6, {0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}};
static const GUID lc_IID_Texture = {0x6f15aaf2, 0xd208, 0x4e89, {0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}};

typedef struct {
	int w, h;
} lc_size;

typedef void *lc_hstring;
typedef HRESULT(WINAPI *ro_init_fn)(int);
typedef HRESULT(WINAPI *ro_factory_fn)(lc_hstring, REFIID, void **);
typedef HRESULT(WINAPI *str_create_fn)(const WCHAR *, UINT32, lc_hstring *);
typedef HRESULT(WINAPI *str_delete_fn)(lc_hstring);
typedef HRESULT(WINAPI *d3d_device_fn)(void *, void **);

// A method of a COM object, by its place in the vtable: IInspectable's own
// six come before those of a WinRT interface, IUnknown's three before those
// of a classic one.
#define LC_VT(o, i) ((*(void ***)(o))[i])
#define LC_CALL(o, i, type, ...) (((type)LC_VT(o, i))((o), ##__VA_ARGS__))

typedef HRESULT(STDMETHODCALLTYPE *m_qi)(void *, REFIID, void **);
typedef ULONG(STDMETHODCALLTYPE *m_release)(void *);
typedef HRESULT(STDMETHODCALLTYPE *m_none)(void *);
typedef HRESULT(STDMETHODCALLTYPE *m_out)(void *, void **);
typedef HRESULT(STDMETHODCALLTYPE *m_size)(void *, lc_size *);
typedef HRESULT(STDMETHODCALLTYPE *m_flag)(void *, unsigned char);
typedef HRESULT(STDMETHODCALLTYPE *m_for_monitor)(void *, HMONITOR, REFIID, void **);
typedef HRESULT(STDMETHODCALLTYPE *m_pool_new)(void *, void *, int, int, lc_size, void **);
typedef HRESULT(STDMETHODCALLTYPE *m_pool_recreate)(void *, void *, int, int, lc_size);
typedef HRESULT(STDMETHODCALLTYPE *m_session_new)(void *, void *, void **);
typedef HRESULT(STDMETHODCALLTYPE *m_get_interface)(void *, REFIID, void **);

#define LC_QI(o, iid, out) LC_CALL(o, 0, m_qi, iid, out)
#define LC_FREE(p) \
	do { \
		if (p) { \
			LC_CALL(p, 2, m_release); \
			(p) = NULL; \
		} \
	} while (0)

#define LC_BGRA 87 // DirectXPixelFormat.B8G8R8A8UIntNormalized
#define LC_BUFFERS 2

static ro_factory_fn ro_factory;
static str_create_fn str_create;
static str_delete_fn str_delete;
static d3d_device_fn d3d_device;

static int wgc_load(void) {
	static int loaded, ok;
	if (loaded) {
		return ok;
	}
	loaded = 1;
	HMODULE combase = LoadLibraryW(L"combase.dll");
	HMODULE d3d = LoadLibraryW(L"d3d11.dll");
	if (!combase || !d3d) {
		return 0;
	}
	ro_init_fn ro_init = (ro_init_fn)GetProcAddress(combase, "RoInitialize");
	ro_factory = (ro_factory_fn)GetProcAddress(combase, "RoGetActivationFactory");
	str_create = (str_create_fn)GetProcAddress(combase, "WindowsCreateString");
	str_delete = (str_delete_fn)GetProcAddress(combase, "WindowsDeleteString");
	d3d_device = (d3d_device_fn)GetProcAddress(d3d, "CreateDirect3D11DeviceFromDXGIDevice");
	if (!ro_init || !ro_factory || !str_create || !str_delete || !d3d_device) {
		return 0;
	}
	// This thread joins the multithreaded apartment and stays in it, which
	// keeps the apartment there for whichever thread captures next.
	ro_init(1);
	ok = 1;
	return 1;
}

static HRESULT factory(const WCHAR *name, UINT32 len, REFIID iid, void **out) {
	lc_hstring s = NULL;
	HRESULT hr = str_create(name, len, &s);
	if (SUCCEEDED(hr)) {
		hr = ro_factory(s, iid, out);
		str_delete(s);
	}
	return hr;
}

struct lc_wgc {
	void *device, *item, *pool, *session;
	void *frame; // held while its texture is read
	lc_size size;
};

// closed closes a WinRT object, which its last reference alone does not.
static void closed(void *o) {
	void *c = NULL;
	if (o && SUCCEEDED(LC_QI(o, &lc_IID_Closable, &c)) && c) {
		LC_CALL(c, 6, m_none);
		LC_FREE(c);
	}
}

void lc_wgc_close(lc_wgc *c) {
	if (!c) {
		return;
	}
	closed(c->frame);
	LC_FREE(c->frame);
	closed(c->session);
	LC_FREE(c->session);
	closed(c->pool);
	LC_FREE(c->pool);
	LC_FREE(c->item);
	LC_FREE(c->device);
	free(c);
}

lc_wgc *lc_wgc_open(void *dev, void *monitor) {
	if (GetEnvironmentVariableW(L"LINKCORD_NO_WGC", NULL, 0) != 0 || !wgc_load()) {
		return NULL;
	}
	lc_wgc *c = calloc(1, sizeof *c);
	void *dxgi = NULL, *interop = NULL, *statics = NULL, *s2 = NULL, *s3 = NULL;
	IUnknown *d = dev;
	int ok = 0;
	if (FAILED(d->lpVtbl->QueryInterface(d, &lc_IID_DXGIDevice, &dxgi)) || FAILED(d3d_device(dxgi, &c->device))) {
		goto done;
	}
	static const WCHAR item[] = L"Windows.Graphics.Capture.GraphicsCaptureItem";
	static const WCHAR pool[] = L"Windows.Graphics.Capture.Direct3D11CaptureFramePool";
	if (FAILED(factory(item, (UINT32)(sizeof item / sizeof item[0] - 1), &lc_IID_ItemInterop, &interop)) ||
	    FAILED(LC_CALL(interop, 4, m_for_monitor, (HMONITOR)monitor, &lc_IID_Item, &c->item)) ||
	    FAILED(LC_CALL(c->item, 7, m_size, &c->size)) ||
	    FAILED(factory(pool, (UINT32)(sizeof pool / sizeof pool[0] - 1), &lc_IID_PoolStatics2, &statics)) ||
	    FAILED(LC_CALL(statics, 6, m_pool_new, c->device, LC_BGRA, LC_BUFFERS, c->size, &c->pool)) ||
	    FAILED(LC_CALL(c->pool, 10, m_session_new, c->item, &c->session))) {
		goto done;
	}
	// The pointer is drawn by the capturer, as for desktop duplication: a
	// Windows that cannot leave it out would show it twice.
	if (FAILED(LC_QI(c->session, &lc_IID_Session2, &s2)) || FAILED(LC_CALL(s2, 7, m_flag, 0))) {
		goto done;
	}
	// No yellow frame around the screen, where Windows allows that.
	if (SUCCEEDED(LC_QI(c->session, &lc_IID_Session3, &s3))) {
		LC_CALL(s3, 7, m_flag, 0);
	}
	ok = SUCCEEDED(LC_CALL(c->session, 6, m_none));
done:
	LC_FREE(s3);
	LC_FREE(s2);
	LC_FREE(statics);
	LC_FREE(interop);
	LC_FREE(dxgi);
	if (!ok) {
		lc_wgc_close(c);
		return NULL;
	}
	return c;
}

// lc_wgc_next takes the newest picture, dropping older ones: 1 with its
// texture, to be released and followed by lc_wgc_done; 0 with none new; -1
// when capturing stopped.
int lc_wgc_next(lc_wgc *c, void **tex) {
	void *frame = NULL;
	for (;;) {
		void *f = NULL;
		if (FAILED(LC_CALL(c->pool, 7, m_out, &f))) {
			closed(frame);
			LC_FREE(frame);
			return -1;
		}
		if (!f) {
			break;
		}
		closed(frame);
		LC_FREE(frame);
		frame = f;
	}
	if (!frame) {
		return 0;
	}
	lc_size size;
	void *surface = NULL, *access = NULL;
	int r = -1;
	if (SUCCEEDED(LC_CALL(frame, 8, m_size, &size)) && (size.w != c->size.w || size.h != c->size.h)) {
		// The screen changed size: the pool follows, the next picture fits.
		c->size = size;
		r = SUCCEEDED(LC_CALL(c->pool, 6, m_pool_recreate, c->device, LC_BGRA, LC_BUFFERS, size)) ? 0 : -1;
	} else if (SUCCEEDED(LC_CALL(frame, 6, m_out, &surface)) && SUCCEEDED(LC_QI(surface, &lc_IID_Access, &access)) &&
	           SUCCEEDED(LC_CALL(access, 3, m_get_interface, &lc_IID_Texture, tex))) {
		c->frame = frame;
		frame = NULL;
		r = 1;
	}
	LC_FREE(access);
	LC_FREE(surface);
	closed(frame);
	LC_FREE(frame);
	return r;
}

void lc_wgc_done(lc_wgc *c) {
	closed(c->frame);
	LC_FREE(c->frame);
}
