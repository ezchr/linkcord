//go:build !no_voice

/* Builds libopus's SSE4.1 code, only run on processors that have it. */

#pragma GCC target("sse4.1")
#include "config.h"
#include "../../third_party/libopus/celt/x86/celt_lpc_sse4_1.c"
#include "../../third_party/libopus/celt/x86/pitch_sse4_1.c"
#include "../../third_party/libopus/silk/x86/NSQ_sse4_1.c"
#include "../../third_party/libopus/silk/x86/NSQ_del_dec_sse4_1.c"
#include "../../third_party/libopus/silk/x86/VAD_sse4_1.c"
#include "../../third_party/libopus/silk/x86/VQ_WMat_EC_sse4_1.c"
