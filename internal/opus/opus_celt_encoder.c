//go:build !no_voice

/* Builds libopus's CELT encoder, apart from the rest of CELT as it declares
   functions the decoder declares too. */

#include "config.h"
#include "../../third_party/libopus/celt/celt_encoder.c"
