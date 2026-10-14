//go:build windows && !no_voice

// The sound of some processes, as Windows 10 2004 and later capture it, for
// a stream to carry the sound of what it shows and not the voice channel's.
// The function that starts it is looked up when used, as Windows 7 has
// none.

#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <objbase.h>
#include <mmdeviceapi.h>
#include <audioclient.h>
#include <stdlib.h>
#include <string.h>

#include "native.h"

typedef enum { LC_ACTIVATION_TYPE_PROCESS_LOOPBACK = 1 } lc_activation_type;
typedef enum { LC_LOOPBACK_INCLUDE = 0, LC_LOOPBACK_EXCLUDE = 1 } lc_loopback_mode;
typedef struct {
	DWORD TargetProcessId;
	lc_loopback_mode ProcessLoopbackMode;
} lc_loopback_params;
typedef struct {
	lc_activation_type ActivationType;
	lc_loopback_params ProcessLoopbackParams;
} lc_activation_params;

static const IID lc_IID_IUnknown = {0x00000000, 0x0000, 0x0000, {0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}};
static const IID lc_IID_IAgileObject = {0x94ea2b94, 0xe9cc, 0x49e0, {0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}};
static const IID lc_IID_Handler = {0x41D949AB, 0x9862, 0x444A, {0x80, 0xF6, 0xC2, 0x61, 0x33, 0x4D, 0xA5, 0xEB}};
static const IID lc_IID_IAudioClient = {0x1CB9AD4C, 0xDBFA, 0x4c32, {0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}};
static const IID lc_IID_IAudioCaptureClient = {0xC8ADBD64, 0xE71E, 0x48a0, {0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}};

typedef HRESULT(WINAPI *activate_fn)(LPCWSTR, REFIID, PROPVARIANT *, IActivateAudioInterfaceCompletionHandler *, IActivateAudioInterfaceAsyncOperation **);

// handler is told when the audio client is ready.
typedef struct {
	IActivateAudioInterfaceCompletionHandler iface;
	LONG ref;
	HANDLE done;
} handler;

static ULONG STDMETHODCALLTYPE h_addref(IActivateAudioInterfaceCompletionHandler *This) {
	return InterlockedIncrement(&((handler *)This)->ref);
}

static ULONG STDMETHODCALLTYPE h_release(IActivateAudioInterfaceCompletionHandler *This) {
	return InterlockedDecrement(&((handler *)This)->ref);
}

static HRESULT STDMETHODCALLTYPE h_qi(IActivateAudioInterfaceCompletionHandler *This, REFIID riid, void **ppv) {
	if (IsEqualIID(riid, &lc_IID_IUnknown) || IsEqualIID(riid, &lc_IID_Handler) || IsEqualIID(riid, &lc_IID_IAgileObject)) {
		*ppv = This;
		h_addref(This);
		return S_OK;
	}
	*ppv = NULL;
	return E_NOINTERFACE;
}

static HRESULT STDMETHODCALLTYPE h_completed(IActivateAudioInterfaceCompletionHandler *This, IActivateAudioInterfaceAsyncOperation *op) {
	(void)op;
	SetEvent(((handler *)This)->done);
	return S_OK;
}

static IActivateAudioInterfaceCompletionHandlerVtbl handler_vtbl = {h_qi, h_addref, h_release, h_completed};

struct lc_ploop {
	IAudioClient *client;
	IAudioCaptureClient *capture;
	int com;
};

lc_ploop *lc_ploop_open(uint32_t pid, int include, int *err) {
	*err = -1;
	HMODULE mm = LoadLibraryW(L"mmdevapi.dll");
	activate_fn activate = mm ? (activate_fn)GetProcAddress(mm, "ActivateAudioInterfaceAsync") : NULL;
	if (!activate) {
		return NULL;
	}
	lc_ploop *c = calloc(1, sizeof *c);
	HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
	c->com = SUCCEEDED(hr);

	lc_activation_params params;
	memset(&params, 0, sizeof params);
	params.ActivationType = LC_ACTIVATION_TYPE_PROCESS_LOOPBACK;
	params.ProcessLoopbackParams.TargetProcessId = pid;
	params.ProcessLoopbackParams.ProcessLoopbackMode = include ? LC_LOOPBACK_INCLUDE : LC_LOOPBACK_EXCLUDE;
	PROPVARIANT pv;
	PropVariantInit(&pv);
	pv.vt = VT_BLOB;
	pv.blob.cbSize = sizeof params;
	pv.blob.pBlobData = (BYTE *)&params;

	handler h = {{&handler_vtbl}, 1, CreateEventW(NULL, FALSE, FALSE, NULL)};
	IActivateAudioInterfaceAsyncOperation *op = NULL;
	hr = activate(L"VAD\\Process_Loopback", &lc_IID_IAudioClient, &pv, &h.iface, &op);
	if (FAILED(hr)) {
		*err = (int)hr;
		goto fail;
	}
	if (WaitForSingleObject(h.done, 5000) != WAIT_OBJECT_0) {
		*err = -2;
		IActivateAudioInterfaceAsyncOperation_Release(op);
		goto fail;
	}
	HRESULT result = E_FAIL;
	IUnknown *unk = NULL;
	hr = IActivateAudioInterfaceAsyncOperation_GetActivateResult(op, &result, &unk);
	IActivateAudioInterfaceAsyncOperation_Release(op);
	if (FAILED(hr) || FAILED(result) || !unk) {
		*err = (int)(FAILED(hr) ? hr : result);
		goto fail;
	}
	hr = IUnknown_QueryInterface(unk, &lc_IID_IAudioClient, (void **)&c->client);
	IUnknown_Release(unk);
	if (FAILED(hr)) {
		*err = (int)hr;
		goto fail;
	}
	WAVEFORMATEX wf;
	memset(&wf, 0, sizeof wf);
	wf.wFormatTag = WAVE_FORMAT_PCM;
	wf.nChannels = 2;
	wf.nSamplesPerSec = 48000;
	wf.wBitsPerSample = 16;
	wf.nBlockAlign = 4;
	wf.nAvgBytesPerSec = 48000 * 4;
	// 200 ms of buffer, in units of 100 ns; Windows converts what plays to
	// this format.
	hr = IAudioClient_Initialize(c->client, AUDCLNT_SHAREMODE_SHARED,
		AUDCLNT_STREAMFLAGS_LOOPBACK | 0x80000000 /* AUTOCONVERTPCM */ | 0x08000000 /* SRC_DEFAULT_QUALITY */,
		2000000, 0, &wf, NULL);
	if (FAILED(hr)) {
		*err = (int)hr;
		goto fail;
	}
	hr = IAudioClient_GetService(c->client, &lc_IID_IAudioCaptureClient, (void **)&c->capture);
	if (FAILED(hr)) {
		*err = (int)hr;
		goto fail;
	}
	hr = IAudioClient_Start(c->client);
	if (FAILED(hr)) {
		*err = (int)hr;
		goto fail;
	}
	CloseHandle(h.done);
	*err = 0;
	return c;

fail:
	CloseHandle(h.done);
	lc_ploop_close(c);
	return NULL;
}

int lc_ploop_read(lc_ploop *c, int16_t *dst, int max) {
	int got = 0;
	while (got < max) {
		UINT32 next = 0;
		if (FAILED(IAudioCaptureClient_GetNextPacketSize(c->capture, &next))) {
			return -1;
		}
		if (next == 0 || got + (int)next > max) {
			break;
		}
		BYTE *data = NULL;
		UINT32 frames = 0;
		DWORD flags = 0;
		if (FAILED(IAudioCaptureClient_GetBuffer(c->capture, &data, &frames, &flags, NULL, NULL))) {
			return -1;
		}
		if (flags & AUDCLNT_BUFFERFLAGS_SILENT) {
			memset(dst + got * 2, 0, (size_t)frames * 4);
		} else {
			memcpy(dst + got * 2, data, (size_t)frames * 4);
		}
		got += frames;
		IAudioCaptureClient_ReleaseBuffer(c->capture, frames);
	}
	return got;
}

void lc_ploop_close(lc_ploop *c) {
	if (!c) {
		return;
	}
	if (c->client) {
		IAudioClient_Stop(c->client);
	}
	if (c->capture) {
		IAudioCaptureClient_Release(c->capture);
	}
	if (c->client) {
		IAudioClient_Release(c->client);
	}
	if (c->com) {
		CoUninitialize();
	}
	free(c);
}
