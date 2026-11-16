// Builds part of WebRTC's AEC3: cgo compiles only the files of this folder.
#include <cstdint>

#include "webrtc/modules/audio_processing/aec3/matched_filter_lag_aggregator.cc"
#include "webrtc/modules/audio_processing/aec3/moving_average.cc"
#include "webrtc/modules/audio_processing/aec3/multi_channel_content_detector.cc"
#include "webrtc/modules/audio_processing/aec3/refined_filter_update_gain.cc"
#include "webrtc/modules/audio_processing/aec3/render_buffer.cc"
#include "webrtc/modules/audio_processing/aec3/render_delay_buffer.cc"
#include "webrtc/modules/audio_processing/aec3/render_delay_controller.cc"
#include "webrtc/modules/audio_processing/aec3/render_delay_controller_metrics.cc"
#include "webrtc/modules/audio_processing/aec3/render_signal_analyzer.cc"
#include "webrtc/modules/audio_processing/aec3/residual_echo_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/reverb_decay_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/reverb_frequency_response.cc"
#include "webrtc/modules/audio_processing/aec3/reverb_model.cc"
#include "webrtc/modules/audio_processing/aec3/reverb_model_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/signal_dependent_erle_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/spectrum_buffer.cc"
