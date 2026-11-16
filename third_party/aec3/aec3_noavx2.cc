// The AVX2 variants of AEC3's inner loops are not built: their files need
// -mavx2 for the whole file, which cgo cannot give to some files only.
// WEBRTC_ENABLE_AVX2 is not defined, so AEC3 never picks them and uses
// SSE2; these only satisfy the linker.
#include <cstdlib>

#include "common_audio/resampler/sinc_resampler.h"
#include "modules/audio_processing/aec3/adaptive_fir_filter.h"
#include "modules/audio_processing/aec3/adaptive_fir_filter_erl.h"
#include "modules/audio_processing/aec3/fft_data.h"
#include "modules/audio_processing/aec3/matched_filter.h"
#include "modules/audio_processing/aec3/vector_math.h"

namespace webrtc {

void FftData::SpectrumAVX2(rtc::ArrayView<float>) const { abort(); }

float SincResampler::Convolve_AVX2(const float*, const float*, const float*, double) { abort(); }

namespace aec3 {

void VectorMath::SqrtAVX2(rtc::ArrayView<float>) { abort(); }
void VectorMath::MultiplyAVX2(rtc::ArrayView<const float>, rtc::ArrayView<const float>, rtc::ArrayView<float>) { abort(); }
void VectorMath::AccumulateAVX2(rtc::ArrayView<const float>, rtc::ArrayView<float>) { abort(); }

void ComputeFrequencyResponse_Avx2(size_t, const std::vector<std::vector<FftData>>&, std::vector<std::array<float, kFftLengthBy2Plus1>>*) { abort(); }
void AdaptPartitions_Avx2(const RenderBuffer&, const FftData&, size_t, std::vector<std::vector<FftData>>*) { abort(); }
void ApplyFilter_Avx2(const RenderBuffer&, size_t, const std::vector<std::vector<FftData>>&, FftData*) { abort(); }
void ErlComputer_AVX2(const std::vector<std::array<float, kFftLengthBy2Plus1>>&, rtc::ArrayView<float>) { abort(); }
void MatchedFilterCore_AVX2(size_t, float, float, rtc::ArrayView<const float>, rtc::ArrayView<const float>, rtc::ArrayView<float>, bool*, float*, bool, rtc::ArrayView<float>, rtc::ArrayView<float>) { abort(); }

}  // namespace aec3
}  // namespace webrtc
