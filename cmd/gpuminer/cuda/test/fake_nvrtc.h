#ifndef FAKE_NVRTC_H
#define FAKE_NVRTC_H

#include <stddef.h>
#include <stdint.h>

#include "../cu.h"

#ifdef __cplusplus
extern "C" {
#endif

/* Fake NVRTC error codes (top of cu.h only has NVRTC_SUCCESS / COMPILATION). */
#define NVRTC_ERROR_INVALID_INPUT         3
#define NVRTC_ERROR_NAME_NOT_EXPRESSED  10
#define NVRTC_ERROR_INTERNAL            11

/* One-shot failure injection (called by fake_cuda_fail_next). */
void fake_nvrtc_fail_next(int op, int err);

/* ------------------------------------------------------------------ */
/* NVRTC API surface                                                   */
/* ------------------------------------------------------------------ */

nvrtcResult nvrtcCreateProgram(nvrtcProgram* program, const char* src,
                               const char* name, int num_headers,
                               const char** headers, const char** include_names);
nvrtcResult nvrtcAddNameExpression(nvrtcProgram program,
                                   const char* name_expression);
nvrtcResult nvrtcGetLoweredName(nvrtcProgram program,
                                const char* expression, const char** lowered);
nvrtcResult nvrtcCompileProgram(nvrtcProgram program, int num_options,
                                const char** options);
nvrtcResult nvrtcGetPTXSize(nvrtcProgram program, size_t* size);
nvrtcResult nvrtcGetPTX(nvrtcProgram program, char* ptx);
nvrtcResult nvrtcGetProgramLogSize(nvrtcProgram program, size_t* size);
nvrtcResult nvrtcGetProgramLog(nvrtcProgram program, char* log);
nvrtcResult nvrtcDestroyProgram(nvrtcProgram* program);
const char* nvrtcGetErrorString(nvrtcResult result);

#ifdef __cplusplus
}
#endif

#endif /* FAKE_NVRTC_H */