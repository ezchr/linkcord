// Builds part of WebRTC's AEC3: cgo compiles only the files of this folder.
#include <cstdint>

#include "webrtc/modules/audio_processing/aec3/decimator.cc"
#include "webrtc/modules/audio_processing/aec3/dominant_nearend_detector.cc"
#include "webrtc/modules/audio_processing/aec3/downsampled_render_buffer.cc"
#include "webrtc/modules/audio_processing/aec3/echo_audibility.cc"
#include "webrtc/modules/audio_processing/aec3/echo_canceller3.cc"
#include "webrtc/modules/audio_processing/aec3/echo_path_delay_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/echo_path_variability.cc"
#include "webrtc/modules/audio_processing/aec3/echo_remover.cc"
#include "webrtc/modules/audio_processing/aec3/echo_remover_metrics.cc"
#include "webrtc/modules/audio_processing/aec3/erl_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/erle_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/fft_buffer.cc"
#include "webrtc/modules/audio_processing/aec3/filter_analyzer.cc"
#include "webrtc/modules/audio_processing/aec3/frame_blocker.cc"
#include "webrtc/modules/audio_processing/aec3/fullband_erle_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/matched_filter.cc"
