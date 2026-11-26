// Package opus encodes and decodes Opus with libopus, the reference codec
// Discord's clients use, built from the sources in third_party/libopus
// (BSD-3-Clause, see third_party/libopus/COPYING) by the opus_*.c files of
// this folder. It is built in floating point, with libopus's SSE4.1 and
// AVX2 code chosen at run time by what the processor has; config.h has the
// rest of how. Built with the no_voice tag, the package is empty.
package opus
