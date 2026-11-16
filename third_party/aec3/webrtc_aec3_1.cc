// Builds part of WebRTC's AEC3: cgo compiles only the files of this folder.
#include <cstdint>

#include "webrtc/modules/audio_processing/aec3/adaptive_fir_filter.cc"
#include "webrtc/modules/audio_processing/aec3/adaptive_fir_filter_erl.cc"
#include "webrtc/modules/audio_processing/aec3/aec3_common.cc"
#include "webrtc/modules/audio_processing/aec3/aec3_fft.cc"
#include "webrtc/modules/audio_processing/aec3/aec_state.cc"
#include "webrtc/modules/audio_processing/aec3/alignment_mixer.cc"
#include "webrtc/modules/audio_processing/aec3/api_call_jitter_metrics.cc"
#include "webrtc/modules/audio_processing/aec3/block_buffer.cc"
#include "webrtc/modules/audio_processing/aec3/block_delay_buffer.cc"
#include "webrtc/modules/audio_processing/aec3/block_framer.cc"
#include "webrtc/modules/audio_processing/aec3/block_processor.cc"
#include "webrtc/modules/audio_processing/aec3/block_processor_metrics.cc"
#include "webrtc/modules/audio_processing/aec3/clockdrift_detector.cc"
#include "webrtc/modules/audio_processing/aec3/coarse_filter_update_gain.cc"
#include "webrtc/modules/audio_processing/aec3/comfort_noise_generator.cc"
#include "webrtc/modules/audio_processing/aec3/config_selector.cc"
