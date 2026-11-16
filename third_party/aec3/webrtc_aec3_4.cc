// Builds part of WebRTC's AEC3: cgo compiles only the files of this folder.
#include <cstdint>

#include "webrtc/modules/audio_processing/aec3/stationarity_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/subband_erle_estimator.cc"
#include "webrtc/modules/audio_processing/aec3/subband_nearend_detector.cc"
#include "webrtc/modules/audio_processing/aec3/subtractor.cc"
#include "webrtc/modules/audio_processing/aec3/subtractor_output.cc"
#include "webrtc/modules/audio_processing/aec3/subtractor_output_analyzer.cc"
#include "webrtc/modules/audio_processing/aec3/suppression_filter.cc"
#include "webrtc/modules/audio_processing/aec3/suppression_gain.cc"
#include "webrtc/modules/audio_processing/aec3/transparent_mode.cc"
