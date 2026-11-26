//go:build !no_voice

/* Builds libopus's AVX2 code, only run on processors that have it. */

#pragma GCC target("avx,avx2,fma")
#include "config.h"
#include "../../third_party/libopus/celt/x86/pitch_avx.c"
#include "../../third_party/libopus/silk/x86/NSQ_del_dec_avx2.c"
#include "../../third_party/libopus/silk/float/x86/inner_product_FLP_avx2.c"
