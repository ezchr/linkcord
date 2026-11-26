//go:build !no_voice

/* Builds libopus's Opus layer, which chooses between SILK and CELT, and its analysis of what is encoded. */

#include "config.h"
#include "../../third_party/libopus/src/opus.c"
#include "../../third_party/libopus/src/opus_decoder.c"
#include "../../third_party/libopus/src/opus_encoder.c"
#include "../../third_party/libopus/src/extensions.c"
#include "../../third_party/libopus/src/opus_multistream.c"
#include "../../third_party/libopus/src/opus_multistream_encoder.c"
#include "../../third_party/libopus/src/opus_multistream_decoder.c"
#include "../../third_party/libopus/src/repacketizer.c"
#include "../../third_party/libopus/src/opus_projection_encoder.c"
#include "../../third_party/libopus/src/opus_projection_decoder.c"
#include "../../third_party/libopus/src/mapping_matrix.c"
#include "../../third_party/libopus/src/analysis.c"
#include "../../third_party/libopus/src/mlp.c"
#include "../../third_party/libopus/src/mlp_data.c"
