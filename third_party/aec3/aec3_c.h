// A C interface to WebRTC's AEC3, for cgo.
#ifndef LC_AEC3_C_H
#define LC_AEC3_C_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct lc_aec3 lc_aec3;

// lc_aec3_new returns a canceller of 48 kHz sound: what is played is mono,
// what is captured has capture_channels channels.
lc_aec3 *lc_aec3_new(int capture_channels);
void lc_aec3_free(lc_aec3 *a);
// lc_aec3_render takes 10 ms, 480 samples, of what is played. It may be
// called while lc_aec3_capture runs, from one thread at a time.
void lc_aec3_render(lc_aec3 *a, const int16_t *pcm);
// lc_aec3_capture removes the echo from 10 ms of what was captured, 480
// samples of each channel, interleaved, in place.
void lc_aec3_capture(lc_aec3 *a, int16_t *pcm);
// lc_aec3_metrics gives AEC3's own estimates: how much quieter the echo
// arrives than it was played and how much of it is removed, in dB, and the
// delay it found between the two, in ms.
void lc_aec3_metrics(lc_aec3 *a, double *erl, double *erle, int *delay_ms);

#ifdef __cplusplus
}
#endif

#endif
