//go:build windows && !no_voice

// The graphics card's own H264 encoder, through Media Foundation: AMD's
// AMF, NVIDIA's NVENC and Intel's Quick Sync all show up there as hardware
// transforms, on Windows 7 as on 11. Media Foundation's DLL is loaded at run
// time, so Linkcord still starts where it is missing (Windows N without the
// Media Feature Pack); the stream then encodes with OpenH264.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mftransform.h>
#include <mferror.h>
#include <strmif.h>
#include <d3d11.h>
#include <dxgi.h>
#include <stdlib.h>
#include <string.h>

#include "native.h"

// Codec API settings, as codecapi.h has them; its C declarations leave the
// GUIDs out.
#define LC_GUID(name, l, w1, w2, b1, b2, b3, b4, b5, b6, b7, b8) \
	static const GUID name = {l, w1, w2, {b1, b2, b3, b4, b5, b6, b7, b8}}
LC_GUID(lc_RateControlMode, 0x1c0608e9, 0x370c, 0x4710, 0x8a, 0x58, 0xcb, 0x61, 0x81, 0xc4, 0x24, 0x23);
LC_GUID(lc_MeanBitRate, 0xf7222374, 0x2144, 0x4815, 0xb5, 0x50, 0xa3, 0x7f, 0x8e, 0x12, 0xee, 0x52);
LC_GUID(lc_MaxBitRate, 0x9651eae4, 0x39b9, 0x4ebf, 0x85, 0xef, 0xd7, 0xf4, 0x44, 0xec, 0x74, 0x65);
LC_GUID(lc_BufferSize, 0x0db96574, 0xb6a4, 0x4c8b, 0x81, 0x06, 0x37, 0x73, 0xde, 0x03, 0x10, 0xcd);
LC_GUID(lc_LowLatencyMode, 0x9c27891a, 0xed7a, 0x40e1, 0x88, 0xe8, 0xb2, 0x27, 0x27, 0xa0, 0x24, 0xee);
LC_GUID(lc_ForceKeyFrame, 0x398c1b98, 0x8353, 0x475a, 0x9e, 0xf2, 0x8f, 0x26, 0x5d, 0x26, 0x03, 0x45);
LC_GUID(lc_GOPSize, 0x95f31b26, 0x95a4, 0x41aa, 0x93, 0x03, 0x24, 0x6a, 0x7f, 0xc6, 0xee, 0xf1);
LC_GUID(lc_BPictureCount, 0x8d390aac, 0xdc5c, 0x4200, 0xb5, 0x7f, 0x81, 0x4d, 0x04, 0xba, 0xba, 0xb2);
// For handing the encoder Direct3D 11 textures (Windows 8 and later).
LC_GUID(lc_IID_IMFDXGIDeviceManager, 0xeb533d5d, 0x2db6, 0x40f8, 0x97, 0xa9, 0x49, 0x46, 0x92, 0x01, 0x4f, 0x07);
LC_GUID(lc_IID_ID3D10Multithread, 0x9b7e4e00, 0x342c, 0x4106, 0xa1, 0x9f, 0x4f, 0x27, 0x04, 0xf6, 0x89, 0xf0);
LC_GUID(lc_IID_ID3D11Texture2D_enc, 0x6f15aaf2, 0xd208, 0x4e89, 0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c);
LC_GUID(lc_IID_IDXGIFactory1_enc, 0x770aae78, 0xf26f, 0x4dba, 0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87);
LC_GUID(lc_MFT_ENUM_ADAPTER_LUID, 0x1d39518c, 0xe220, 0x4da8, 0xa0, 0x7f, 0xba, 0x17, 0x25, 0x52, 0xd6, 0xb1);

// ID3D10Multithread, which d3d10.h has, cut down to the call made: the
// encoder's threads use the device as Linkcord's does.
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

typedef HRESULT(WINAPI *mf_create_dxgi_manager_fn)(UINT *, IMFDXGIDeviceManager **);
typedef HRESULT(WINAPI *mf_create_dxgi_buffer_fn)(REFIID, IUnknown *, UINT, BOOL, IMFMediaBuffer **);
typedef HRESULT(WINAPI *d3d11_create_device_fn)(IDXGIAdapter *, D3D_DRIVER_TYPE, HMODULE, UINT, const D3D_FEATURE_LEVEL *, UINT, UINT, ID3D11Device **, D3D_FEATURE_LEVEL *, ID3D11DeviceContext **);
typedef HRESULT(WINAPI *dxgi_create_factory_fn)(REFIID, void **);

typedef HRESULT(WINAPI *mf_startup_fn)(ULONG, DWORD);
typedef HRESULT(WINAPI *mft_enum_ex_fn)(GUID, UINT32, const MFT_REGISTER_TYPE_INFO *, const MFT_REGISTER_TYPE_INFO *, IMFActivate ***, UINT32 *);
typedef HRESULT(WINAPI *mf_create_media_type_fn)(IMFMediaType **);
typedef HRESULT(WINAPI *mf_create_sample_fn)(IMFSample **);
typedef HRESULT(WINAPI *mf_create_memory_buffer_fn)(DWORD, IMFMediaBuffer **);

static struct {
	SRWLOCK lock;
	int state; // 0 not tried, 1 loaded, -1 not available
	mft_enum_ex_fn enum_ex;
	mf_create_media_type_fn create_media_type;
	mf_create_sample_fn create_sample;
	mf_create_memory_buffer_fn create_memory_buffer;
	// Windows 8 and later; NULL before.
	mf_create_dxgi_manager_fn create_dxgi_manager;
	mf_create_dxgi_buffer_fn create_dxgi_buffer;
	d3d11_create_device_fn create_device;
	dxgi_create_factory_fn create_factory;
} mf = {SRWLOCK_INIT};

// mf_load loads Media Foundation once and reports whether it could.
static int mf_load(void) {
	AcquireSRWLockExclusive(&mf.lock);
	if (mf.state == 0) {
		mf.state = -1;
		HMODULE m = LoadLibraryW(L"mfplat.dll");
		if (m) {
			mf_startup_fn startup = (mf_startup_fn)GetProcAddress(m, "MFStartup");
			mf.enum_ex = (mft_enum_ex_fn)GetProcAddress(m, "MFTEnumEx");
			mf.create_media_type = (mf_create_media_type_fn)GetProcAddress(m, "MFCreateMediaType");
			mf.create_sample = (mf_create_sample_fn)GetProcAddress(m, "MFCreateSample");
			mf.create_memory_buffer = (mf_create_memory_buffer_fn)GetProcAddress(m, "MFCreateMemoryBuffer");
			mf.create_dxgi_manager = (mf_create_dxgi_manager_fn)GetProcAddress(m, "MFCreateDXGIDeviceManager");
			mf.create_dxgi_buffer = (mf_create_dxgi_buffer_fn)GetProcAddress(m, "MFCreateDXGISurfaceBuffer");
			HMODULE d3d = LoadLibraryW(L"d3d11.dll"), dxgi = LoadLibraryW(L"dxgi.dll");
			if (d3d && dxgi) {
				mf.create_device = (d3d11_create_device_fn)GetProcAddress(d3d, "D3D11CreateDevice");
				mf.create_factory = (dxgi_create_factory_fn)GetProcAddress(dxgi, "CreateDXGIFactory1");
			}
			// MFSTARTUP_LITE: no sockets, which an encoder does not need.
			if (startup && mf.enum_ex && mf.create_media_type && mf.create_sample && mf.create_memory_buffer &&
			    SUCCEEDED(startup(MF_VERSION, MFSTARTUP_LITE))) {
				mf.state = 1;
			}
		}
	}
	int ok = mf.state == 1;
	ReleaseSRWLockExclusive(&mf.lock);
	return ok;
}

#define LC_HWENC_QUEUE 8
// Textures the pictures go through to the encoder, used in turn: more than
// a low-latency encoder ever holds at once.
#define LC_HWENC_SLOTS 6

typedef struct {
	uint8_t *data;
	int len, cap, idr;
	int64_t ts_ms;
} lc_hwout;

struct lc_hwenc {
	IMFActivate *act;
	IMFTransform *t;
	IMFMediaEventGenerator *ev; // set for asynchronous transforms, as all hardware ones are
	ICodecAPI *api;
	int w, h, fps;
	DWORD in_id, out_id;
	int need_input;           // inputs the transform asked for and has not had
	int provides_samples;     // the transform makes its own output samples
	DWORD out_size;           // else, how big a sample to give it
	// Pictures encoded and not returned yet; taken: the first was returned
	// and is freed on the next call.
	lc_hwout q[LC_HWENC_QUEUE];
	int q0, nq, taken;
	int com; // CoInitializeEx succeeded on the opening thread
	uint16_t name[128];
	// With d3d, pictures go to the encoder as textures of dev: written
	// through staging, copied on the graphics card into tex.
	int d3d, slot;
	ID3D11Device *dev;
	ID3D11DeviceContext *ctx;
	IMFDXGIDeviceManager *mgr;
	ID3D11Texture2D *staging[LC_HWENC_SLOTS], *tex[LC_HWENC_SLOTS];
};

static void set_u32(ICodecAPI *api, const GUID *g, ULONG v) {
	VARIANT var;
	VariantInit(&var);
	var.vt = VT_UI4;
	var.ulVal = v;
	ICodecAPI_SetValue(api, g, &var);
}

static void set_bool(ICodecAPI *api, const GUID *g, int v) {
	VARIANT var;
	VariantInit(&var);
	var.vt = VT_BOOL;
	var.boolVal = v ? VARIANT_TRUE : VARIANT_FALSE;
	ICodecAPI_SetValue(api, g, &var);
}

static void set_rate(lc_hwenc *c, int bitrate) {
	if (!c->api) {
		return;
	}
	// Constant bitrate, as Discord's streams: steady for the network. The
	// peak and the buffer of a second leave the room Discord found keyframes
	// need, rather than starving them to hold every picture to its share.
	set_u32(c->api, &lc_RateControlMode, 0 /* eAVEncCommonRateControlMode_CBR */);
	set_u32(c->api, &lc_MeanBitRate, (ULONG)bitrate);
	set_u32(c->api, &lc_MaxBitRate, (ULONG)(bitrate * 3 / 2));
	set_u32(c->api, &lc_BufferSize, (ULONG)bitrate);
}

static IMFMediaType *video_type(lc_hwenc *c, const GUID *subtype) {
	IMFMediaType *t = NULL;
	if (FAILED(mf.create_media_type(&t))) {
		return NULL;
	}
	IMFMediaType_SetGUID(t, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
	IMFMediaType_SetGUID(t, &MF_MT_SUBTYPE, subtype);
	IMFMediaType_SetUINT64(t, &MF_MT_FRAME_SIZE, ((UINT64)c->w << 32) | (UINT32)c->h);
	IMFMediaType_SetUINT64(t, &MF_MT_FRAME_RATE, ((UINT64)c->fps << 32) | 1);
	IMFMediaType_SetUINT64(t, &MF_MT_PIXEL_ASPECT_RATIO, ((UINT64)1 << 32) | 1);
	IMFMediaType_SetUINT32(t, &MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
	return t;
}

static void release_d3d(lc_hwenc *c) {
	for (int i = 0; i < LC_HWENC_SLOTS; i++) {
		if (c->staging[i]) {
			ID3D11Texture2D_Release(c->staging[i]);
		}
		if (c->tex[i]) {
			ID3D11Texture2D_Release(c->tex[i]);
		}
		c->staging[i] = c->tex[i] = NULL;
	}
	if (c->mgr) {
		IMFDXGIDeviceManager_Release(c->mgr);
	}
	if (c->ctx) {
		ID3D11DeviceContext_Release(c->ctx);
	}
	if (c->dev) {
		ID3D11Device_Release(c->dev);
	}
	c->mgr = NULL;
	c->ctx = NULL;
	c->dev = NULL;
	c->d3d = 0;
}

// adapter returns the graphics card whose encoder act is, or NULL for the
// default one.
static IDXGIAdapter *adapter(IMFActivate *act) {
	UINT64 luid = 0;
	if (!mf.create_factory || FAILED(IMFActivate_GetUINT64(act, &lc_MFT_ENUM_ADAPTER_LUID, &luid))) {
		return NULL;
	}
	IDXGIFactory1 *f = NULL;
	if (FAILED(mf.create_factory(&lc_IID_IDXGIFactory1_enc, (void **)&f))) {
		return NULL;
	}
	IDXGIAdapter1 *a = NULL, *found = NULL;
	for (UINT i = 0; !found && IDXGIFactory1_EnumAdapters1(f, i, &a) == S_OK; i++) {
		DXGI_ADAPTER_DESC1 d;
		if (SUCCEEDED(IDXGIAdapter1_GetDesc1(a, &d)) && ((UINT64)(UINT32)d.AdapterLuid.HighPart << 32 | d.AdapterLuid.LowPart) == luid) {
			found = a;
		} else {
			IDXGIAdapter1_Release(a);
		}
	}
	IDXGIFactory1_Release(f);
	return (IDXGIAdapter *)found;
}

// use_d3d has the transform take Direct3D 11 textures, on the graphics card
// it encodes on: the way it works best, without copying each picture in
// software. It reports whether it could; Windows 7 cannot.
static int use_d3d(lc_hwenc *c, IMFActivate *act) {
	if (!mf.create_dxgi_manager || !mf.create_dxgi_buffer || !mf.create_device) {
		return 0;
	}
	IDXGIAdapter *a = adapter(act);
	HRESULT hr = mf.create_device(a, a ? D3D_DRIVER_TYPE_UNKNOWN : D3D_DRIVER_TYPE_HARDWARE, NULL,
	                              D3D11_CREATE_DEVICE_VIDEO_SUPPORT | D3D11_CREATE_DEVICE_BGRA_SUPPORT, NULL, 0, D3D11_SDK_VERSION, &c->dev, NULL, &c->ctx);
	if (a) {
		IDXGIAdapter_Release(a);
	}
	if (FAILED(hr)) {
		return 0;
	}
	lc_mt *mt = NULL;
	if (SUCCEEDED(ID3D11Device_QueryInterface(c->dev, &lc_IID_ID3D10Multithread, (void **)&mt))) {
		mt->lpVtbl->SetMultithreadProtected(mt, TRUE);
		mt->lpVtbl->Release(mt);
	}
	UINT token = 0;
	if (FAILED(mf.create_dxgi_manager(&token, &c->mgr)) ||
	    FAILED(IMFDXGIDeviceManager_ResetDevice(c->mgr, (IUnknown *)c->dev, token)) ||
	    FAILED(IMFTransform_ProcessMessage(c->t, MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)c->mgr))) {
		release_d3d(c);
		return 0;
	}
	D3D11_TEXTURE2D_DESC d;
	memset(&d, 0, sizeof d);
	d.Width = (UINT)c->w;
	d.Height = (UINT)c->h;
	d.MipLevels = 1;
	d.ArraySize = 1;
	d.Format = DXGI_FORMAT_NV12;
	d.SampleDesc.Count = 1;
	for (int i = 0; i < LC_HWENC_SLOTS; i++) {
		d.Usage = D3D11_USAGE_STAGING;
		d.BindFlags = 0;
		d.CPUAccessFlags = D3D11_CPU_ACCESS_WRITE;
		if (FAILED(ID3D11Device_CreateTexture2D(c->dev, &d, NULL, &c->staging[i]))) {
			goto fail;
		}
		d.Usage = D3D11_USAGE_DEFAULT;
		d.CPUAccessFlags = 0;
		d.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
		if (FAILED(ID3D11Device_CreateTexture2D(c->dev, &d, NULL, &c->tex[i]))) {
			d.BindFlags = 0;
			if (FAILED(ID3D11Device_CreateTexture2D(c->dev, &d, NULL, &c->tex[i]))) {
				goto fail;
			}
		}
	}
	c->d3d = 1;
	return 1;
fail:
	// The transform keeps the manager it was given: it is told there is none.
	IMFTransform_ProcessMessage(c->t, MFT_MESSAGE_SET_D3D_MANAGER, 0);
	release_d3d(c);
	return 0;
}

// d3d_sample puts the NV12 picture in the next texture and returns a sample
// of it for the encoder, or NULL on failure.
static IMFSample *d3d_sample(lc_hwenc *c, const uint8_t *nv12) {
	int i = c->slot;
	c->slot = (c->slot + 1) % LC_HWENC_SLOTS;
	D3D11_MAPPED_SUBRESOURCE m;
	if (FAILED(ID3D11DeviceContext_Map(c->ctx, (ID3D11Resource *)c->staging[i], 0, D3D11_MAP_WRITE, 0, &m))) {
		return NULL;
	}
	// NV12 maps as one block: the Y rows, then as many half-height UV rows.
	uint8_t *dst = m.pData;
	for (int y = 0; y < c->h; y++) {
		memcpy(dst + (size_t)y * m.RowPitch, nv12 + (size_t)y * c->w, (size_t)c->w);
	}
	const uint8_t *uv = nv12 + (size_t)c->w * c->h;
	dst += (size_t)m.RowPitch * c->h;
	for (int y = 0; y < c->h / 2; y++) {
		memcpy(dst + (size_t)y * m.RowPitch, uv + (size_t)y * c->w, (size_t)c->w);
	}
	ID3D11DeviceContext_Unmap(c->ctx, (ID3D11Resource *)c->staging[i], 0);
	ID3D11DeviceContext_CopyResource(c->ctx, (ID3D11Resource *)c->tex[i], (ID3D11Resource *)c->staging[i]);

	IMFMediaBuffer *b = NULL;
	if (FAILED(mf.create_dxgi_buffer(&lc_IID_ID3D11Texture2D_enc, (IUnknown *)c->tex[i], 0, FALSE, &b))) {
		return NULL;
	}
	DWORD max = 0;
	if (SUCCEEDED(IMFMediaBuffer_GetMaxLength(b, &max))) {
		IMFMediaBuffer_SetCurrentLength(b, max);
	}
	IMFSample *s = NULL;
	if (FAILED(mf.create_sample(&s))) {
		IMFMediaBuffer_Release(b);
		return NULL;
	}
	IMFSample_AddBuffer(s, b);
	IMFMediaBuffer_Release(b);
	return s;
}

static void release(lc_hwenc *c) {
	if (c->api) {
		ICodecAPI_Release(c->api);
	}
	if (c->ev) {
		IMFMediaEventGenerator_Release(c->ev);
	}
	if (c->t) {
		IMFTransform_Release(c->t);
	}
	if (c->act) {
		IMFActivate_ShutdownObject(c->act);
		IMFActivate_Release(c->act);
	}
	c->api = NULL;
	c->ev = NULL;
	c->t = NULL;
	c->act = NULL;
	release_d3d(c);
}

// setup makes the transform of act encode as c asks, and reports whether it
// could.
static int setup(lc_hwenc *c, IMFActivate *act, int bitrate, int idr) {
	IMFTransform *t = NULL;
	if (FAILED(IMFActivate_ActivateObject(act, &IID_IMFTransform, (void **)&t))) {
		return 0;
	}
	c->act = act;
	IMFActivate_AddRef(act);
	c->t = t;
	UINT32 len = 0;
	IMFActivate_GetString(act, &MFT_FRIENDLY_NAME_Attribute, (LPWSTR)c->name, 127, &len);

	IMFAttributes *attrs = NULL;
	if (SUCCEEDED(IMFTransform_GetAttributes(t, &attrs)) && attrs) {
		UINT32 async = 0;
		if (SUCCEEDED(IMFAttributes_GetUINT32(attrs, &MF_TRANSFORM_ASYNC, &async)) && async) {
			IMFAttributes_SetUINT32(attrs, &MF_TRANSFORM_ASYNC_UNLOCK, TRUE);
			IMFTransform_QueryInterface(t, &IID_IMFMediaEventGenerator, (void **)&c->ev);
		}
		// MF_LOW_LATENCY, which has the GUID of the codec API's low latency mode.
		IMFAttributes_SetUINT32(attrs, &lc_LowLatencyMode, TRUE);
		IMFAttributes_Release(attrs);
	}
	// Textures where the system has them, which must come before the types.
	use_d3d(c, act);
	IMFTransform_QueryInterface(t, &IID_ICodecAPI, (void **)&c->api);
	if (c->api) {
		set_bool(c->api, &lc_LowLatencyMode, 1);
		set_u32(c->api, &lc_GOPSize, (ULONG)idr);
		set_u32(c->api, &lc_BPictureCount, 0);
		set_rate(c, bitrate);
	}

	if (FAILED(IMFTransform_GetStreamIDs(t, 1, &c->in_id, 1, &c->out_id))) {
		c->in_id = c->out_id = 0;
	}
	// An encoder takes its output type first. Baseline, as OpenH264 sends:
	// every viewer decodes it.
	IMFMediaType *out = video_type(c, &MFVideoFormat_H264);
	if (!out) {
		return 0;
	}
	IMFMediaType_SetUINT32(out, &MF_MT_AVG_BITRATE, (UINT32)bitrate);
	IMFMediaType_SetUINT32(out, &MF_MT_MPEG2_PROFILE, 66 /* eAVEncH264VProfile_Base */);
	HRESULT hr = IMFTransform_SetOutputType(t, c->out_id, out, 0);
	IMFMediaType_Release(out);
	if (FAILED(hr)) {
		return 0;
	}
	IMFMediaType *in = video_type(c, &MFVideoFormat_NV12);
	if (!in) {
		return 0;
	}
	hr = IMFTransform_SetInputType(t, c->in_id, in, 0);
	IMFMediaType_Release(in);
	if (FAILED(hr)) {
		return 0;
	}

	MFT_OUTPUT_STREAM_INFO info;
	memset(&info, 0, sizeof info);
	if (FAILED(IMFTransform_GetOutputStreamInfo(t, c->out_id, &info))) {
		return 0;
	}
	c->provides_samples = (info.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES | MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES)) != 0;
	c->out_size = info.cbSize ? info.cbSize : (DWORD)(c->w * c->h * 3 / 2);

	IMFTransform_ProcessMessage(t, MFT_MESSAGE_COMMAND_FLUSH, 0);
	if (FAILED(IMFTransform_ProcessMessage(t, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0)) ||
	    FAILED(IMFTransform_ProcessMessage(t, MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0))) {
		return 0;
	}
	return 1;
}

lc_hwenc *lc_hwenc_open(int w, int h, int fps, int bitrate, int idr, uint16_t *name, int name_len) {
	if (!mf_load()) {
		return NULL;
	}
	lc_hwenc *c = calloc(1, sizeof *c);
	c->w = w;
	c->h = h;
	c->fps = fps;
	HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
	c->com = SUCCEEDED(hr);

	MFT_REGISTER_TYPE_INFO in = {MFMediaType_Video, MFVideoFormat_NV12};
	MFT_REGISTER_TYPE_INFO out = {MFMediaType_Video, MFVideoFormat_H264};
	IMFActivate **acts = NULL;
	UINT32 n = 0;
	hr = mf.enum_ex(MFT_CATEGORY_VIDEO_ENCODER, MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER, &in, &out, &acts, &n);
	int ok = 0;
	if (SUCCEEDED(hr)) {
		for (UINT32 i = 0; i < n; i++) {
			if (!ok) {
				ok = setup(c, acts[i], bitrate, idr);
				if (!ok) {
					release(c);
				}
			}
			IMFActivate_Release(acts[i]);
		}
		CoTaskMemFree(acts);
	}
	if (!ok) {
		if (c->com) {
			CoUninitialize();
		}
		free(c);
		return NULL;
	}
	if (name && name_len > 0) {
		int i = 0;
		for (; i < name_len - 1 && c->name[i]; i++) {
			name[i] = c->name[i];
		}
		// How pictures reach it, for the log.
		const char *how = c->d3d ? " (D3D11 textures)" : " (system memory)";
		for (int j = 0; how[j] && i < name_len - 1; j++, i++) {
			name[i] = (uint16_t)how[j];
		}
		name[i] = 0;
	}
	return c;
}

// take queues a copy of an output sample.
static void take(lc_hwenc *c, IMFSample *s) {
	if (c->nq == LC_HWENC_QUEUE) {
		return; // never: the encoder holds a picture or two at most
	}
	IMFMediaBuffer *b = NULL;
	if (FAILED(IMFSample_ConvertToContiguousBuffer(s, &b))) {
		return;
	}
	BYTE *p = NULL;
	DWORD len = 0;
	if (SUCCEEDED(IMFMediaBuffer_Lock(b, &p, NULL, &len))) {
		if (len > 0) {
			lc_hwout *o = &c->q[(c->q0 + c->nq) % LC_HWENC_QUEUE];
			if ((int)len > o->cap) {
				free(o->data);
				o->cap = (int)len * 2;
				o->data = malloc(o->cap);
			}
			if (o->data) {
				memcpy(o->data, p, len);
				o->len = (int)len;
				LONGLONG t = 0;
				o->ts_ms = SUCCEEDED(IMFSample_GetSampleTime(s, &t)) ? t / 10000 : 0;
				UINT32 clean = 0;
				o->idr = SUCCEEDED(IMFSample_GetUINT32(s, &MFSampleExtension_CleanPoint, &clean)) && clean;
				c->nq++;
			}
		}
		IMFMediaBuffer_Unlock(b);
	}
	IMFMediaBuffer_Release(b);
}

// pull takes a picture the transform encoded into the queue. It returns 1
// with one, 0 when the transform needs more pictures first, -1 on failure.
static int pull(lc_hwenc *c) {
	MFT_OUTPUT_DATA_BUFFER db;
	memset(&db, 0, sizeof db);
	db.dwStreamID = c->out_id;
	IMFSample *mine = NULL;
	if (!c->provides_samples) {
		IMFMediaBuffer *b = NULL;
		if (FAILED(mf.create_sample(&mine)) || FAILED(mf.create_memory_buffer(c->out_size, &b))) {
			if (mine) {
				IMFSample_Release(mine);
			}
			return -1;
		}
		IMFSample_AddBuffer(mine, b);
		IMFMediaBuffer_Release(b);
		db.pSample = mine;
	}
	DWORD status = 0;
	HRESULT hr = IMFTransform_ProcessOutput(c->t, 0, 1, &db, &status);
	if (db.pEvents) {
		IMFCollection_Release(db.pEvents);
	}
	int r;
	if (hr == MF_E_TRANSFORM_NEED_MORE_INPUT) {
		r = 0;
	} else if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
		// The encoder settled its output type: take it and go on.
		IMFMediaType *t = NULL;
		if (SUCCEEDED(IMFTransform_GetOutputAvailableType(c->t, c->out_id, 0, &t))) {
			IMFTransform_SetOutputType(c->t, c->out_id, t, 0);
			IMFMediaType_Release(t);
		}
		r = 0;
	} else if (FAILED(hr)) {
		r = -1;
	} else {
		if (db.pSample) {
			take(c, db.pSample);
		}
		r = 1;
	}
	if (db.pSample && db.pSample != mine) {
		IMFSample_Release(db.pSample);
	}
	if (mine) {
		IMFSample_Release(mine);
	}
	return r;
}

// next waits for the transform's next event: 1 it wants a picture, 2 it has
// one encoded, 0 for others, -1 on failure.
static int next(lc_hwenc *c) {
	IMFMediaEvent *e = NULL;
	if (FAILED(IMFMediaEventGenerator_GetEvent(c->ev, 0, &e))) {
		return -1;
	}
	MediaEventType type = MEUnknown;
	IMFMediaEvent_GetType(e, &type);
	HRESULT status = S_OK;
	IMFMediaEvent_GetStatus(e, &status);
	IMFMediaEvent_Release(e);
	if (FAILED(status)) {
		return -1;
	}
	switch (type) {
	case METransformNeedInput:
		return 1;
	case METransformHaveOutput:
		return 2;
	default:
		return 0;
	}
}

// handle acts on one event of an asynchronous transform.
static int handle(lc_hwenc *c, int e) {
	if (e == 1) {
		c->need_input++;
	} else if (e == 2 && pull(c) < 0) {
		return -1;
	}
	return e < 0 ? -1 : 0;
}

int lc_hwenc_encode(lc_hwenc *c, const uint8_t *nv12, int64_t ts_ms, int force_idr, uint8_t **out, int *out_len, int64_t *out_ts_ms, int *idr) {
	*out = NULL;
	*out_len = 0;
	*idr = 0;
	*out_ts_ms = ts_ms;
	// The last picture returned is done with.
	if (c->taken) {
		c->q0 = (c->q0 + 1) % LC_HWENC_QUEUE;
		c->nq--;
		c->taken = 0;
	}
	// An asynchronous encoder says when it takes a picture; until then, what
	// it has encoded is collected.
	while (c->ev && c->need_input == 0) {
		if (handle(c, next(c)) < 0) {
			return -1;
		}
	}

	IMFSample *s = NULL;
	if (c->d3d) {
		if (!(s = d3d_sample(c, nv12))) {
			return -1;
		}
	} else {
		DWORD size = (DWORD)(c->w * c->h * 3 / 2);
		IMFMediaBuffer *b = NULL;
		if (FAILED(mf.create_memory_buffer(size, &b))) {
			return -1;
		}
		BYTE *p = NULL;
		if (FAILED(IMFMediaBuffer_Lock(b, &p, NULL, NULL))) {
			IMFMediaBuffer_Release(b);
			return -1;
		}
		memcpy(p, nv12, size);
		IMFMediaBuffer_Unlock(b);
		IMFMediaBuffer_SetCurrentLength(b, size);
		if (FAILED(mf.create_sample(&s))) {
			IMFMediaBuffer_Release(b);
			return -1;
		}
		IMFSample_AddBuffer(s, b);
		IMFMediaBuffer_Release(b);
	}
	IMFSample_SetSampleTime(s, ts_ms * 10000);
	IMFSample_SetSampleDuration(s, 10000000LL / (c->fps > 0 ? c->fps : 30));
	if (force_idr && c->api) {
		set_u32(c->api, &lc_ForceKeyFrame, 1);
	}
	HRESULT hr = IMFTransform_ProcessInput(c->t, c->in_id, s, 0);
	IMFSample_Release(s);
	if (FAILED(hr)) {
		return -1;
	}

	if (c->ev) {
		c->need_input--;
		// Low latency: the picture just given is usually ready at once. If
		// the encoder asks for another first, it holds this one back and a
		// later call returns it.
		while (c->nq == 0 && c->need_input == 0) {
			if (handle(c, next(c)) < 0) {
				return -1;
			}
		}
	} else {
		int r;
		while ((r = pull(c)) == 1) {
		}
		if (r < 0) {
			return -1;
		}
	}
	if (c->nq > 0) {
		lc_hwout *o = &c->q[c->q0];
		*out = o->data;
		*out_len = o->len;
		*out_ts_ms = o->ts_ms;
		*idr = o->idr;
		c->taken = 1;
	}
	return 0;
}
void lc_hwenc_set_bitrate(lc_hwenc *c, int bitrate) {
	set_rate(c, bitrate);
}

void lc_hwenc_close(lc_hwenc *c) {
	if (!c) {
		return;
	}
	if (c->t) {
		IMFTransform_ProcessMessage(c->t, MFT_MESSAGE_NOTIFY_END_OF_STREAM, 0);
		IMFTransform_ProcessMessage(c->t, MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
	}
	release(c);
	if (c->com) {
		CoUninitialize();
	}
	for (int i = 0; i < LC_HWENC_QUEUE; i++) {
		free(c->q[i].data);
	}
	free(c);
}
