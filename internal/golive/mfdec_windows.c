//go:build windows && !no_voice

// Decoding a watched stream inside Linkcord, as Discord's client does:
// Media Foundation's H264 decoder decodes on the graphics card through
// Direct3D 11, and the card's video processor scales each picture to the
// size it is shown at and converts it to RGBA. Only that picture, often far
// smaller than the stream, is read back. Everything is loaded at run time,
// so Linkcord still starts where it is missing; where the graphics card
// cannot decode this way (Windows 7, which has no Direct3D 11 video
// decoding, or Windows N without the Media Feature Pack), opening fails and
// the stream is decoded by FFmpeg instead.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mftransform.h>
#include <mferror.h>
#include <mfobjects.h>
#include <strmif.h>
#include <d3d11.h>
#include <stdlib.h>
#include <string.h>

#include "native.h"

#define LC_DGUID(name, l, w1, w2, b1, b2, b3, b4, b5, b6, b7, b8) \
	static const GUID name = {l, w1, w2, {b1, b2, b3, b4, b5, b6, b7, b8}}
LC_DGUID(lc_CLSID_H264Decoder, 0x62ce7e72, 0x4c71, 0x4d20, 0xb1, 0x5d, 0x45, 0x28, 0x31, 0xa8, 0x7d, 0x9d);
LC_DGUID(lc_dec_LowLatencyMode, 0x9c27891a, 0xed7a, 0x40e1, 0x88, 0xe8, 0xb2, 0x27, 0x27, 0xa0, 0x24, 0xee);
LC_DGUID(lc_dec_D3D11Aware, 0x206b4fc8, 0xfcf9, 0x4c51, 0xaf, 0xe3, 0x97, 0x64, 0x36, 0x9e, 0x33, 0xa0);
LC_DGUID(lc_dec_IID_IMFDXGIBuffer, 0xe7174cfa, 0x1c9e, 0x48b1, 0x88, 0x66, 0x62, 0x62, 0x26, 0xbf, 0xc2, 0x58);
LC_DGUID(lc_dec_IID_ID3D11Texture2D, 0x6f15aaf2, 0xd208, 0x4e89, 0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c);
LC_DGUID(lc_dec_IID_ID3D11VideoDevice, 0x10ec4d5b, 0x975a, 0x4689, 0xb9, 0xe4, 0xd0, 0xaa, 0xc3, 0x0f, 0xe3, 0x33);
LC_DGUID(lc_dec_IID_ID3D11VideoContext, 0x61f21c45, 0x3c0e, 0x4a74, 0x9c, 0xea, 0x67, 0x10, 0x0d, 0x9a, 0xd5, 0xe4);

typedef HRESULT(WINAPI *dec_startup_fn)(ULONG, DWORD);
typedef HRESULT(WINAPI *dec_media_type_fn)(IMFMediaType **);
typedef HRESULT(WINAPI *dec_sample_fn)(IMFSample **);
typedef HRESULT(WINAPI *dec_memory_buffer_fn)(DWORD, IMFMediaBuffer **);
typedef HRESULT(WINAPI *dec_dxgi_manager_fn)(UINT *, IMFDXGIDeviceManager **);
typedef HRESULT(WINAPI *dec_device_fn)(IDXGIAdapter *, D3D_DRIVER_TYPE, HMODULE, UINT, const D3D_FEATURE_LEVEL *, UINT, UINT, ID3D11Device **, D3D_FEATURE_LEVEL *, ID3D11DeviceContext **);

static struct {
	SRWLOCK lock;
	int state; // 0 not tried, 1 loaded, -1 not available
	dec_media_type_fn media_type;
	dec_sample_fn sample;
	dec_memory_buffer_fn memory_buffer;
	dec_dxgi_manager_fn dxgi_manager;
	dec_device_fn device;
} dl = {SRWLOCK_INIT};

// dec_load loads Media Foundation and Direct3D 11 once, and reports whether
// everything decoding on the graphics card needs is there: the DXGI device
// manager only Windows 8 and later have.
static int dec_load(void) {
	AcquireSRWLockExclusive(&dl.lock);
	if (dl.state == 0) {
		dl.state = -1;
		HMODULE m = LoadLibraryW(L"mfplat.dll");
		HMODULE d3d = LoadLibraryW(L"d3d11.dll");
		if (m && d3d) {
			dec_startup_fn startup = (dec_startup_fn)GetProcAddress(m, "MFStartup");
			dl.media_type = (dec_media_type_fn)GetProcAddress(m, "MFCreateMediaType");
			dl.sample = (dec_sample_fn)GetProcAddress(m, "MFCreateSample");
			dl.memory_buffer = (dec_memory_buffer_fn)GetProcAddress(m, "MFCreateMemoryBuffer");
			dl.dxgi_manager = (dec_dxgi_manager_fn)GetProcAddress(m, "MFCreateDXGIDeviceManager");
			dl.device = (dec_device_fn)GetProcAddress(d3d, "D3D11CreateDevice");
			if (startup && dl.media_type && dl.sample && dl.memory_buffer && dl.dxgi_manager && dl.device &&
			    SUCCEEDED(startup(MF_VERSION, MFSTARTUP_LITE))) {
				dl.state = 1;
			}
		}
	}
	int ok = dl.state == 1;
	ReleaseSRWLockExclusive(&dl.lock);
	return ok;
}

// The decoder's pictures are slices of a few array textures; the input
// views of the video processor are kept for each one seen.
#define LC_DEC_VIEWS 48

// A graphics card's video processor may cost far more shrinking a picture
// to less than half: an RX 6650 XT took 6 times the processor time from
// 1080 to 480 lines as to 540. So a picture is halved in stages, each by no
// more than half, which also averages every pixel in as FFmpeg's area
// scaling does.
#define LC_DEC_STAGES 4

typedef struct {
	ID3D11VideoProcessorEnumerator *venum;
	ID3D11VideoProcessor *vp;
	ID3D11Texture2D *out;
	ID3D11VideoProcessorOutputView *outview;
	// After the first stage, the input is the last stage's output.
	ID3D11VideoProcessorInputView *inview;
	int sw, sh, ow, oh;
} lc_stage;

struct lc_hwdec {
	IMFTransform *t;
	ID3D11Device *dev;
	ID3D11DeviceContext *ctx;
	IMFDXGIDeviceManager *mgr;
	int provides; // the decoder makes its own output samples
	DWORD out_size;
	// cw by ch is the picture shown, of the decoder's pictures (which are
	// bigger, to whole macroblocks).
	int cw, ch;
	LONGLONG ts;

	ID3D11VideoDevice *vdev;
	ID3D11VideoContext *vctx;
	// The processors take iw by ih pictures, of which cw by ch shows (as
	// vcw, vch were when they were made), to ow by oh ones, in stages.
	lc_stage st[LC_DEC_STAGES];
	int nst;
	int iw, ih, ow, oh, vcw, vch;
	int rgba; // the output is RGBA rather than BGRA
	ID3D11Texture2D *staging;
	struct {
		ID3D11Texture2D *tex;
		UINT slice;
		ID3D11VideoProcessorInputView *view;
	} views[LC_DEC_VIEWS];
	int nviews;
};

#define LC_DREL(p, fn) \
	do { \
		if (p) { \
			fn(p); \
			(p) = NULL; \
		} \
	} while (0)

static void dec_release_views(lc_hwdec *d) {
	for (int i = 0; i < d->nviews; i++) {
		LC_DREL(d->views[i].view, ID3D11VideoProcessorInputView_Release);
		d->views[i].tex = NULL;
	}
	d->nviews = 0;
}

static void dec_release_stage(lc_stage *g) {
	LC_DREL(g->inview, ID3D11VideoProcessorInputView_Release);
	LC_DREL(g->outview, ID3D11VideoProcessorOutputView_Release);
	LC_DREL(g->out, ID3D11Texture2D_Release);
	LC_DREL(g->vp, ID3D11VideoProcessor_Release);
	LC_DREL(g->venum, ID3D11VideoProcessorEnumerator_Release);
}

static void dec_release_vp(lc_hwdec *d) {
	dec_release_views(d);
	for (int i = 0; i < LC_DEC_STAGES; i++) {
		dec_release_stage(&d->st[i]);
	}
	LC_DREL(d->staging, ID3D11Texture2D_Release);
	d->nst = 0;
	d->iw = d->ih = d->ow = d->oh = 0;
}

void lc_hwdec_close(lc_hwdec *d) {
	if (!d) {
		return;
	}
	dec_release_vp(d);
	LC_DREL(d->vctx, ID3D11VideoContext_Release);
	LC_DREL(d->vdev, ID3D11VideoDevice_Release);
	if (d->t) {
		IMFTransform_ProcessMessage(d->t, MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
		IMFTransform_ProcessMessage(d->t, MFT_MESSAGE_SET_D3D_MANAGER, 0);
	}
	LC_DREL(d->t, IMFTransform_Release);
	LC_DREL(d->mgr, IMFDXGIDeviceManager_Release);
	LC_DREL(d->ctx, ID3D11DeviceContext_Release);
	LC_DREL(d->dev, ID3D11Device_Release);
	free(d);
}

// dec_output_type has the decoder give NV12, the format of the graphics
// card's pictures, and takes the size shown from it.
static int dec_output_type(lc_hwdec *d) {
	for (DWORD i = 0;; i++) {
		IMFMediaType *t = NULL;
		if (FAILED(IMFTransform_GetOutputAvailableType(d->t, 0, i, &t))) {
			return 0;
		}
		GUID sub;
		int ok = SUCCEEDED(IMFMediaType_GetGUID(t, &MF_MT_SUBTYPE, &sub)) && IsEqualGUID(&sub, &MFVideoFormat_NV12) &&
		         SUCCEEDED(IMFTransform_SetOutputType(d->t, 0, t, 0));
		if (ok) {
			UINT64 size = 0;
			IMFMediaType_GetUINT64(t, &MF_MT_FRAME_SIZE, &size);
			d->cw = (int)(size >> 32);
			d->ch = (int)(UINT32)size;
			MFVideoArea area;
			UINT32 n = 0;
			if (SUCCEEDED(IMFMediaType_GetBlob(t, &MF_MT_MINIMUM_DISPLAY_APERTURE, (UINT8 *)&area, sizeof area, &n)) &&
			    n == sizeof area && area.Area.cx > 0 && area.Area.cy > 0) {
				d->cw = (int)area.Area.cx;
				d->ch = (int)area.Area.cy;
			}
			MFT_OUTPUT_STREAM_INFO info;
			memset(&info, 0, sizeof info);
			IMFTransform_GetOutputStreamInfo(d->t, 0, &info);
			d->provides = (info.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES | MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES)) != 0;
			d->out_size = info.cbSize;
		}
		IMFMediaType_Release(t);
		if (ok) {
			// The decoder's pictures are new textures now.
			dec_release_views(d);
			return 1;
		}
	}
}

lc_hwdec *lc_hwdec_open(int w, int h) {
	CoInitializeEx(NULL, COINIT_MULTITHREADED);
	if (!dec_load()) {
		return NULL;
	}
	lc_hwdec *d = calloc(1, sizeof *d);
	if (!d) {
		return NULL;
	}
	if (FAILED(CoCreateInstance(&lc_CLSID_H264Decoder, NULL, CLSCTX_INPROC_SERVER, &IID_IMFTransform, (void **)&d->t))) {
		goto fail;
	}
	IMFAttributes *attrs = NULL;
	UINT32 aware = 0;
	if (FAILED(IMFTransform_GetAttributes(d->t, &attrs)) || !attrs) {
		goto fail;
	}
	IMFAttributes_GetUINT32(attrs, &lc_dec_D3D11Aware, &aware);
	// A picture out for each one in, rather than a few held back.
	IMFAttributes_SetUINT32(attrs, &lc_dec_LowLatencyMode, TRUE);
	IMFAttributes_Release(attrs);
	if (!aware) {
		goto fail;
	}
	ICodecAPI *api = NULL;
	if (SUCCEEDED(IMFTransform_QueryInterface(d->t, &IID_ICodecAPI, (void **)&api)) && api) {
		VARIANT v;
		VariantInit(&v);
		v.vt = VT_BOOL;
		v.boolVal = VARIANT_TRUE;
		ICodecAPI_SetValue(api, &lc_dec_LowLatencyMode, &v);
		ICodecAPI_Release(api);
	}

	if (FAILED(dl.device(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, D3D11_CREATE_DEVICE_VIDEO_SUPPORT | D3D11_CREATE_DEVICE_BGRA_SUPPORT,
	                     NULL, 0, D3D11_SDK_VERSION, &d->dev, NULL, &d->ctx))) {
		goto fail;
	}
	// The decoder's threads use the device as this one does.
	lc_mt_protect(d->dev);
	if (FAILED(ID3D11Device_QueryInterface(d->dev, &lc_dec_IID_ID3D11VideoDevice, (void **)&d->vdev)) ||
	    FAILED(ID3D11DeviceContext_QueryInterface(d->ctx, &lc_dec_IID_ID3D11VideoContext, (void **)&d->vctx))) {
		goto fail;
	}
	UINT token = 0;
	// Without the graphics card, the decoder refuses the manager: then
	// FFmpeg decodes rather than Media Foundation in software.
	if (FAILED(dl.dxgi_manager(&token, &d->mgr)) ||
	    FAILED(IMFDXGIDeviceManager_ResetDevice(d->mgr, (IUnknown *)d->dev, token)) ||
	    FAILED(IMFTransform_ProcessMessage(d->t, MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)d->mgr))) {
		goto fail;
	}

	IMFMediaType *in = NULL;
	if (FAILED(dl.media_type(&in))) {
		goto fail;
	}
	IMFMediaType_SetGUID(in, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
	IMFMediaType_SetGUID(in, &MF_MT_SUBTYPE, &MFVideoFormat_H264);
	IMFMediaType_SetUINT64(in, &MF_MT_FRAME_SIZE, ((UINT64)w << 32) | (UINT32)h);
	IMFMediaType_SetUINT32(in, &MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
	HRESULT hr = IMFTransform_SetInputType(d->t, 0, in, 0);
	IMFMediaType_Release(in);
	if (FAILED(hr) || !dec_output_type(d)) {
		goto fail;
	}
	IMFTransform_ProcessMessage(d->t, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0);
	IMFTransform_ProcessMessage(d->t, MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
	return d;
fail:
	lc_hwdec_close(d);
	return NULL;
}

// dec_stage makes the video processor of a stage, from iw by ih pictures
// of which its sw by sh show, to its ow by oh. The first stage takes the
// decoder's NV12, the others the last one's RGB.
static int dec_stage(lc_hwdec *d, lc_stage *g, int first, int iw, int ih) {
	D3D11_VIDEO_PROCESSOR_CONTENT_DESC cd;
	memset(&cd, 0, sizeof cd);
	cd.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
	cd.InputWidth = (UINT)iw;
	cd.InputHeight = (UINT)ih;
	cd.OutputWidth = (UINT)g->ow;
	cd.OutputHeight = (UINT)g->oh;
	cd.InputFrameRate.Numerator = cd.OutputFrameRate.Numerator = 60;
	cd.InputFrameRate.Denominator = cd.OutputFrameRate.Denominator = 1;
	cd.Usage = D3D11_VIDEO_USAGE_OPTIMAL_SPEED;
	if (FAILED(ID3D11VideoDevice_CreateVideoProcessorEnumerator(d->vdev, &cd, &g->venum)) ||
	    FAILED(ID3D11VideoDevice_CreateVideoProcessor(d->vdev, g->venum, 0, &g->vp))) {
		return 0;
	}
	D3D11_TEXTURE2D_DESC t;
	memset(&t, 0, sizeof t);
	t.Width = (UINT)g->ow;
	t.Height = (UINT)g->oh;
	t.MipLevels = t.ArraySize = 1;
	t.Format = d->rgba ? DXGI_FORMAT_R8G8B8A8_UNORM : DXGI_FORMAT_B8G8R8A8_UNORM;
	t.SampleDesc.Count = 1;
	t.Usage = D3D11_USAGE_DEFAULT;
	t.BindFlags = D3D11_BIND_RENDER_TARGET;
	D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC ov;
	memset(&ov, 0, sizeof ov);
	ov.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
	if (FAILED(ID3D11Device_CreateTexture2D(d->dev, &t, NULL, &g->out)) ||
	    FAILED(ID3D11VideoDevice_CreateVideoProcessorOutputView(d->vdev, (ID3D11Resource *)g->out, g->venum, &ov, &g->outview))) {
		return 0;
	}
	if (!first) {
		D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC iv;
		memset(&iv, 0, sizeof iv);
		iv.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
		if (FAILED(ID3D11VideoDevice_CreateVideoProcessorInputView(d->vdev, (ID3D11Resource *)(g - 1)->out, g->venum, &iv, &g->inview))) {
			return 0;
		}
	}
	// BT.601 limited range in, as FFmpeg takes a stream that does not say,
	// full-range RGB out; RGB stays full range.
	D3D11_VIDEO_PROCESSOR_COLOR_SPACE in, out;
	memset(&in, 0, sizeof in);
	memset(&out, 0, sizeof out);
	in.YCbCr_Matrix = 0;
	in.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_16_235;
	ID3D11VideoContext_VideoProcessorSetStreamColorSpace(d->vctx, g->vp, 0, &in);
	ID3D11VideoContext_VideoProcessorSetOutputColorSpace(d->vctx, g->vp, &out);
	ID3D11VideoContext_VideoProcessorSetStreamAutoProcessingMode(d->vctx, g->vp, 0, FALSE);
	ID3D11VideoContext_VideoProcessorSetStreamFrameFormat(d->vctx, g->vp, 0, D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE);
	RECT src = {0, 0, g->sw, g->sh};
	RECT dst = {0, 0, g->ow, g->oh};
	ID3D11VideoContext_VideoProcessorSetStreamSourceRect(d->vctx, g->vp, 0, TRUE, &src);
	ID3D11VideoContext_VideoProcessorSetStreamDestRect(d->vctx, g->vp, 0, TRUE, &dst);
	ID3D11VideoContext_VideoProcessorSetOutputTargetRect(d->vctx, g->vp, TRUE, &dst);
	return 1;
}

// dec_stages makes every stage, with RGBA output if rgba, and reports
// whether it could, and could with that format.
static int dec_stages(lc_hwdec *d, int iw, int ih, int rgba) {
	d->rgba = rgba;
	for (int i = 0; i < d->nst; i++) {
		lc_stage *g = &d->st[i];
		if (!dec_stage(d, g, i == 0, i == 0 ? iw : d->st[i - 1].ow, i == 0 ? ih : d->st[i - 1].oh)) {
			return 0;
		}
		UINT flags = 0;
		if (rgba && (FAILED(ID3D11VideoProcessorEnumerator_CheckVideoProcessorFormat(g->venum, DXGI_FORMAT_R8G8B8A8_UNORM, &flags)) ||
		             !(flags & D3D11_VIDEO_PROCESSOR_FORMAT_SUPPORT_OUTPUT) || (i > 0 && !(flags & D3D11_VIDEO_PROCESSOR_FORMAT_SUPPORT_INPUT)))) {
			return 0;
		}
	}
	return 1;
}

// dec_vp makes the video processors from the decoder's iw by ih NV12
// pictures to ow by oh RGBA, or BGRA where the graphics card cannot give
// RGBA, and the texture it is read back through.
static int dec_vp(lc_hwdec *d, int iw, int ih, int ow, int oh) {
	if (d->nst && d->iw == iw && d->ih == ih && d->ow == ow && d->oh == oh && d->vcw == d->cw && d->vch == d->ch) {
		return 1;
	}
	dec_release_vp(d);
	int cw = d->cw > 0 && d->cw < iw ? d->cw : iw, ch = d->ch > 0 && d->ch < ih ? d->ch : ih;
	// The stages: each at most halves, the last gives ow by oh.
	int n = 0, w = cw, h = ch;
	while (n < LC_DEC_STAGES - 1 && (w > ow * 2 || h > oh * 2)) {
		lc_stage *g = &d->st[n++];
		g->sw = w;
		g->sh = h;
		w = g->ow = w > ow * 2 ? (w + 1) / 2 : ow;
		h = g->oh = h > oh * 2 ? (h + 1) / 2 : oh;
	}
	d->st[n].sw = w;
	d->st[n].sh = h;
	d->st[n].ow = ow;
	d->st[n].oh = oh;
	d->nst = n + 1;
	// RGBA where every stage can, which spares swapping red and blue here.
	if (!dec_stages(d, iw, ih, 1)) {
		for (int i = 0; i < d->nst; i++) {
			dec_release_stage(&d->st[i]);
		}
		if (!dec_stages(d, iw, ih, 0)) {
			dec_release_vp(d);
			return 0;
		}
	}
	D3D11_TEXTURE2D_DESC t;
	memset(&t, 0, sizeof t);
	t.Width = (UINT)ow;
	t.Height = (UINT)oh;
	t.MipLevels = t.ArraySize = 1;
	t.Format = d->rgba ? DXGI_FORMAT_R8G8B8A8_UNORM : DXGI_FORMAT_B8G8R8A8_UNORM;
	t.SampleDesc.Count = 1;
	t.Usage = D3D11_USAGE_STAGING;
	t.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
	if (FAILED(ID3D11Device_CreateTexture2D(d->dev, &t, NULL, &d->staging))) {
		dec_release_vp(d);
		return 0;
	}
	d->iw = iw;
	d->ih = ih;
	d->ow = ow;
	d->oh = oh;
	d->vcw = d->cw;
	d->vch = d->ch;
	return 1;
}

// dec_view returns the processor's input view of a slice of a decoder's
// texture, made the first time it is seen.
static ID3D11VideoProcessorInputView *dec_view(lc_hwdec *d, ID3D11Texture2D *tex, UINT slice) {
	for (int i = 0; i < d->nviews; i++) {
		if (d->views[i].tex == tex && d->views[i].slice == slice) {
			return d->views[i].view;
		}
	}
	if (d->nviews == LC_DEC_VIEWS) {
		dec_release_views(d);
	}
	D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC iv;
	memset(&iv, 0, sizeof iv);
	iv.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
	iv.Texture2D.ArraySlice = slice;
	ID3D11VideoProcessorInputView *v = NULL;
	if (FAILED(ID3D11VideoDevice_CreateVideoProcessorInputView(d->vdev, (ID3D11Resource *)tex, d->st[0].venum, &iv, &v))) {
		return NULL;
	}
	// The texture is the decoder's, which keeps it while the view lives:
	// the pointer only tells the views apart.
	d->views[d->nviews].tex = tex;
	d->views[d->nviews].slice = slice;
	d->views[d->nviews].view = v;
	d->nviews++;
	return v;
}

// dec_render scales and converts the decoded picture s to ow by oh RGBA
// into rgba, with rows ow*4 bytes apart.
static int dec_render(lc_hwdec *d, IMFSample *s, uint8_t *rgba, int ow, int oh) {
	IMFMediaBuffer *b = NULL;
	if (FAILED(IMFSample_GetBufferByIndex(s, 0, &b))) {
		return -1;
	}
	IMFDXGIBuffer *db = NULL;
	ID3D11Texture2D *tex = NULL;
	UINT slice = 0;
	HRESULT hr = IMFMediaBuffer_QueryInterface(b, &lc_dec_IID_IMFDXGIBuffer, (void **)&db);
	IMFMediaBuffer_Release(b);
	if (FAILED(hr)) {
		return -1;
	}
	hr = IMFDXGIBuffer_GetResource(db, &lc_dec_IID_ID3D11Texture2D, (void **)&tex);
	if (SUCCEEDED(hr)) {
		hr = IMFDXGIBuffer_GetSubresourceIndex(db, &slice);
	}
	IMFDXGIBuffer_Release(db);
	if (FAILED(hr)) {
		if (tex) {
			ID3D11Texture2D_Release(tex);
		}
		return -1;
	}
	D3D11_TEXTURE2D_DESC td;
	ID3D11Texture2D_GetDesc(tex, &td);
	int ret = -1;
	ID3D11VideoProcessorInputView *view;
	if (!dec_vp(d, (int)td.Width, (int)td.Height, ow, oh) || !(view = dec_view(d, tex, slice))) {
		goto done;
	}
	for (int i = 0; i < d->nst; i++) {
		lc_stage *g = &d->st[i];
		D3D11_VIDEO_PROCESSOR_STREAM st;
		memset(&st, 0, sizeof st);
		st.Enable = TRUE;
		st.pInputSurface = i == 0 ? view : g->inview;
		if (FAILED(ID3D11VideoContext_VideoProcessorBlt(d->vctx, g->vp, g->outview, 0, 1, &st))) {
			goto done;
		}
	}
	ID3D11DeviceContext_CopyResource(d->ctx, (ID3D11Resource *)d->staging, (ID3D11Resource *)d->st[d->nst - 1].out);
	D3D11_MAPPED_SUBRESOURCE m;
	if (FAILED(ID3D11DeviceContext_Map(d->ctx, (ID3D11Resource *)d->staging, 0, D3D11_MAP_READ, 0, &m))) {
		goto done;
	}
	size_t row = (size_t)ow * 4;
	for (int y = 0; y < oh; y++) {
		const uint8_t *p = (const uint8_t *)m.pData + (size_t)y * m.RowPitch;
		uint8_t *q = rgba + (size_t)y * row;
		if (d->rgba) {
			memcpy(q, p, row);
		} else {
			for (int x = 0; x < ow; x++, p += 4, q += 4) {
				q[0] = p[2];
				q[1] = p[1];
				q[2] = p[0];
				q[3] = 255;
			}
		}
	}
	ID3D11DeviceContext_Unmap(d->ctx, (ID3D11Resource *)d->staging, 0);
	ret = 0;
done:
	ID3D11Texture2D_Release(tex);
	return ret;
}

// dec_drain takes every picture the decoder has ready, rendering each into
// rgba: the last one stays. It returns 0 or -1.
static int dec_drain(lc_hwdec *d, uint8_t *rgba, int ow, int oh, int *got) {
	for (;;) {
		MFT_OUTPUT_DATA_BUFFER ob;
		memset(&ob, 0, sizeof ob);
		IMFSample *own = NULL;
		if (!d->provides) {
			IMFMediaBuffer *b = NULL;
			if (FAILED(dl.sample(&own)) || FAILED(dl.memory_buffer(d->out_size ? d->out_size : 4 << 20, &b))) {
				if (own) {
					IMFSample_Release(own);
				}
				return -1;
			}
			IMFSample_AddBuffer(own, b);
			IMFMediaBuffer_Release(b);
			ob.pSample = own;
		}
		DWORD status = 0;
		HRESULT hr = IMFTransform_ProcessOutput(d->t, 0, 1, &ob, &status);
		if (ob.pEvents) {
			IMFCollection_Release(ob.pEvents);
		}
		int ret = 0;
		if (hr == MF_E_TRANSFORM_NEED_MORE_INPUT) {
			ret = 1;
		} else if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
			// A new size: the decoder's pictures change with it.
			ret = dec_output_type(d) ? 0 : -1;
		} else if (FAILED(hr)) {
			ret = -1;
		} else if (ob.pSample) {
			if (dec_render(d, ob.pSample, rgba, ow, oh) == 0) {
				*got = 1;
			} else {
				ret = -1;
			}
		}
		if (ob.pSample && ob.pSample != own) {
			IMFSample_Release(ob.pSample);
		}
		if (own) {
			IMFSample_Release(own);
		}
		if (ret != 0) {
			return ret < 0 ? -1 : 0;
		}
	}
}

int lc_hwdec_decode(lc_hwdec *d, const uint8_t *au, int len, uint8_t *rgba, int ow, int oh, int *got) {
	*got = 0;
	if (ow <= 0 || oh <= 0) {
		return -1;
	}
	// Calls may come from any of Go's threads.
	CoInitializeEx(NULL, COINIT_MULTITHREADED);
	IMFMediaBuffer *b = NULL;
	IMFSample *s = NULL;
	if (FAILED(dl.memory_buffer((DWORD)len, &b))) {
		return -1;
	}
	BYTE *p = NULL;
	if (FAILED(IMFMediaBuffer_Lock(b, &p, NULL, NULL))) {
		IMFMediaBuffer_Release(b);
		return -1;
	}
	memcpy(p, au, (size_t)len);
	IMFMediaBuffer_Unlock(b);
	IMFMediaBuffer_SetCurrentLength(b, (DWORD)len);
	if (FAILED(dl.sample(&s))) {
		IMFMediaBuffer_Release(b);
		return -1;
	}
	IMFSample_AddBuffer(s, b);
	IMFMediaBuffer_Release(b);
	// Live pictures have no times of their own: steady ones keep the
	// decoder from reordering or dropping any.
	IMFSample_SetSampleTime(s, d->ts);
	IMFSample_SetSampleDuration(s, 166667);
	d->ts += 166667;
	HRESULT hr = IMFTransform_ProcessInput(d->t, 0, s, 0);
	if (hr == MF_E_NOTACCEPTING) {
		if (dec_drain(d, rgba, ow, oh, got) < 0) {
			IMFSample_Release(s);
			return -1;
		}
		hr = IMFTransform_ProcessInput(d->t, 0, s, 0);
	}
	IMFSample_Release(s);
	if (FAILED(hr)) {
		return -1;
	}
	return dec_drain(d, rgba, ow, oh, got);
}

// lc_hwdec_size reports the size of the picture shown, once known.
void lc_hwdec_size(lc_hwdec *d, int *w, int *h) {
	*w = d->cw;
	*h = d->ch;
}
