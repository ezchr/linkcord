//go:build !no_voice

/* Builds libopus's CELT decoder, apart from the rest of CELT as it declares
   functions the encoder declares too. */

#include "config.h"
#include "../../third_party/libopus/celt/celt_decoder.c"
