// The C interface to AEC3: what WebRTC's audio processing module does
// around its echo canceller, without the rest of the module.
#include "aec3_c.h"

#include <cstdint>
#include <optional>

#include "api/audio/audio_processing.h"
#include "api/audio/echo_canceller3_config.h"
#include "modules/audio_processing/aec3/echo_canceller3.h"
#include "modules/audio_processing/audio_buffer.h"
#include "modules/audio_processing/high_pass_filter.h"

namespace {
const int kRate = 48000;
}

struct lc_aec3 {
  explicit lc_aec3(int channels)
      : channels(channels),
        canceller(webrtc::EchoCanceller3Config(), std::nullopt, kRate, 1, channels),
        render(kRate, 1, kRate, 1, kRate, 1),
        capture(kRate, channels, kRate, channels, kRate, channels),
        high_pass(kRate, channels) {}

  int channels;
  webrtc::EchoCanceller3 canceller;
  // AEC3 works on sound split into three bands of 16 kHz, which these do.
  webrtc::AudioBuffer render, capture;
  // The module always removes what is below speech before AEC3.
  webrtc::HighPassFilter high_pass;
};

extern "C" {

lc_aec3 *lc_aec3_new(int capture_channels) {
  return new lc_aec3(capture_channels);
}

void lc_aec3_free(lc_aec3 *a) { delete a; }

void lc_aec3_render(lc_aec3 *a, const int16_t *pcm) {
  a->render.CopyFrom(pcm, webrtc::StreamConfig(kRate, 1));
  a->render.SplitIntoFrequencyBands();
  a->canceller.AnalyzeRender(&a->render);
}

void lc_aec3_capture(lc_aec3 *a, int16_t *pcm) {
  webrtc::StreamConfig config(kRate, a->channels);
  a->capture.CopyFrom(pcm, config);
  a->canceller.AnalyzeCapture(&a->capture);
  a->capture.SplitIntoFrequencyBands();
  a->high_pass.Process(&a->capture, true);
  a->canceller.ProcessCapture(&a->capture, false);
  a->capture.MergeFrequencyBands();
  a->capture.CopyTo(config, pcm);
}

void lc_aec3_metrics(lc_aec3 *a, double *erl, double *erle, int *delay_ms) {
  webrtc::EchoControl::Metrics m = a->canceller.GetMetrics();
  *erl = m.echo_return_loss;
  *erle = m.echo_return_loss_enhancement;
  *delay_ms = m.delay_ms;
}

}  // extern "C"
