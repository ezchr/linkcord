/* The configuration libopus is built with, in place of the config.h its
   configure script writes: floating point, with the SSE4.1 and AVX2 code
   chosen at run time by what the processor has. SSE and SSE2 are always
   there on x86-64. */
#ifndef LINKCORD_OPUS_CONFIG_H
#define LINKCORD_OPUS_CONFIG_H

#define OPUS_BUILD 1
#define VAR_ARRAYS 1
#define HAVE_LRINT 1
#define HAVE_LRINTF 1
#define HAVE_STDINT_H 1
#define HAVE_STRING_H 1
#define HAVE_STDLIB_H 1
#define PACKAGE_VERSION "1.6.1"

#if defined(__x86_64__) || defined(__i386__)
#define OPUS_HAVE_RTCD 1
#define CPU_INFO_BY_C 1
#define OPUS_X86_MAY_HAVE_SSE 1
#define OPUS_X86_MAY_HAVE_SSE2 1
#define OPUS_X86_MAY_HAVE_SSE4_1 1
#define OPUS_X86_MAY_HAVE_AVX2 1
#if defined(__x86_64__)
#define OPUS_X86_PRESUME_SSE 1
#define OPUS_X86_PRESUME_SSE2 1
#endif
#endif

#endif
