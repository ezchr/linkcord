//go:build !no_voice

/* Builds libopus's CELT layer, the codec of music and of the higher bands. */

#include "config.h"
#include "../../third_party/libopus/celt/bands.c"
#include "../../third_party/libopus/celt/celt.c"
#include "../../third_party/libopus/celt/cwrs.c"
#include "../../third_party/libopus/celt/entcode.c"
#include "../../third_party/libopus/celt/entdec.c"
#include "../../third_party/libopus/celt/entenc.c"
#include "../../third_party/libopus/celt/kiss_fft.c"
#include "../../third_party/libopus/celt/laplace.c"
#include "../../third_party/libopus/celt/mathops.c"
#include "../../third_party/libopus/celt/mdct.c"
#include "../../third_party/libopus/celt/modes.c"
#include "../../third_party/libopus/celt/pitch.c"
#include "../../third_party/libopus/celt/celt_lpc.c"
#include "../../third_party/libopus/celt/quant_bands.c"
#include "../../third_party/libopus/celt/rate.c"
#include "../../third_party/libopus/celt/vq.c"
