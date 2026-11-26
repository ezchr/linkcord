//go:build !no_voice

/* Builds libopus's choice of the x86 code by what the processor has, and its SSE and SSE2 code, always there on x86-64. */

#include "config.h"
#include "../../third_party/libopus/celt/x86/x86cpu.c"
#include "../../third_party/libopus/celt/x86/x86_celt_map.c"
#include "../../third_party/libopus/silk/x86/x86_silk_map.c"
#include "../../third_party/libopus/celt/x86/pitch_sse.c"
#include "../../third_party/libopus/celt/x86/pitch_sse2.c"
#include "../../third_party/libopus/celt/x86/vq_sse2.c"
