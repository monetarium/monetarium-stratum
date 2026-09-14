#ifndef FAKE_CUDA_H
#define FAKE_CUDA_H

#include <stddef.h>
#include <stdint.h>

#include "../cu.h"

#ifdef __cplusplus
extern "C" {
#endif

/*
 * Fake CUDA Driver API for testing cmd/gpuminer/cuda without a GPU, a CUDA
 * runtime or an NVRTC install.  The fake implements the exact API surface the
 * production host (host.cpp) dlopens, dispatching cuLaunchKernel to
 * cpu_reference.c.
 *
 * It is built in two flavors:
 *
 *   1. linked directly into C introspection tests (api_tests.c), and
 *   2. as libcuda.so.1 / libnvrtc.so.12 loaded via LD_LIBRARY_PATH when the
 *      real cuda_host binary is exercised end-to-end.
 *
 * Every exported symbol exists both with its plain name and the CUDA `_v2`
 * alias used by the host's cuSym() fallback chain.
 */

/* ------------------------------------------------------------------ */
/* Introspection / test hooks                                          */
/* ------------------------------------------------------------------ */

typedef enum FakeCudaOperation {
    FAKE_OP_INIT,
    FAKE_OP_DRIVER_VERSION,
    FAKE_OP_DEVICE_GET_COUNT,
    FAKE_OP_DEVICE_GET,
    FAKE_OP_DEVICE_GET_NAME,
    FAKE_OP_DEVICE_GET_ATTRIBUTE,
    FAKE_OP_CTX_CREATE,
    FAKE_OP_CTX_DESTROY,
    FAKE_OP_MODULE_LOAD,
    FAKE_OP_MODULE_GET_FUNCTION,
    FAKE_OP_MODULE_GET_GLOBAL,
    FAKE_OP_MODULE_UNLOAD,
    FAKE_OP_MEM_ALLOC,
    FAKE_OP_MEM_HTOD,
    FAKE_OP_MEM_DTOH,
    FAKE_OP_MEM_MEMSET,
    FAKE_OP_MEM_FREE,
    FAKE_OP_LAUNCH,
    FAKE_OP_SYNC,
    FAKE_OP_GET_ERROR_STRING,
    FAKE_OP_GET_ERROR_NAME,
    FAKE_OP_COUNT
} FakeCudaOperation;

/*
 * One-shot error injection: the next call to FAKE_OP_* returns `err`
 * (0 disables).  Errors are delivered exactly once so a test can trap the
 * first interaction with any subsystem without derailing subsequent calls.
 */
void fake_cuda_fail_next(FakeCudaOperation op, CUresult err);

/*
 * Capture of the most recent cuLaunchKernel invocation.  NULL pointers are
 * ignored.  Returns 0 on success.
 */
int fake_cuda_get_last_launch(unsigned int* grid_x, unsigned int* grid_y,
                              unsigned int* grid_z, unsigned int* block_x,
                              unsigned int* block_y, unsigned int* block_z,
                              unsigned int* shared_bytes);

/* Live-resource accounting for leak checks. */
int fake_cuda_live_contexts(void);
int fake_cuda_live_allocations(void);
int fake_cuda_live_modules(void);
int fake_cuda_live_constants(void);
int fake_cuda_total_allocs(void);

/*
 * Validation/leak assertion.  Returns 0 when live resources are balanced, -1
 * when a leak or an unknown poison value is observed.
 */
int fake_cuda_assert_no_leaks(void);

/* Quantize a device pointer to its backing allocation.  Returns 0 on success. */
int fake_cuda_ptr_info(CUdeviceptr ptr, void** base, size_t* size,
                       int* is_const);

/* Configure the fake device. */
void fake_cuda_set_compute_capability(int major, int minor);
void fake_cuda_set_driver_version(int version);

/* Reset all fake state (leaks are reported first). */
void fake_cuda_reset(void);

/* ------------------------------------------------------------------ */
/* CUDA Driver API surface (plain + _v2 aliases)                       */
/* ------------------------------------------------------------------ */

CUresult cuInit(unsigned int flags);
CUresult cuDriverGetVersion(int* version);
CUresult cuDeviceGetCount(int* count);
CUresult cuDeviceGet(CUdevice* device, int ordinal);
CUresult cuDeviceGetName(char* name, int len, CUdevice device);
CUresult cuDeviceGetAttribute(int* pi, int attrib, CUdevice device);
CUresult cuCtxCreate_v2(CUcontext* pctx, unsigned int flags,
                        CUdevice device);
CUresult cuCtxDestroy_v2(CUcontext ctx);
CUresult cuModuleLoadDataEx(CUmodule* module, const void* image,
                            unsigned int numOptions, int* options,
                            void** optionValues);
CUresult cuModuleGetFunction(CUfunction* func, CUmodule hmod,
                             const char* name);
CUresult cuModuleGetGlobal_v2(CUdeviceptr* dptr, size_t* bytes,
                              CUmodule hmod, const char* name);
CUresult cuModuleUnload(CUmodule hmod);
CUresult cuMemAlloc_v2(CUdeviceptr* dptr, size_t bytes);
CUresult cuMemcpyHtoD_v2(CUdeviceptr dstDevice, const void* srcHost,
                         size_t ByteCount);
CUresult cuMemcpyDtoH_v2(void* dstHost, CUdeviceptr srcDevice,
                         size_t ByteCount);
CUresult cuMemsetD8_v2(CUdeviceptr dstDevice, unsigned char uc,
                       size_t ByteCount);
CUresult cuMemFree(CUdeviceptr dptr);
CUresult cuLaunchKernel(CUfunction f, unsigned int gridDimX,
                        unsigned int gridDimY, unsigned int gridDimZ,
                        unsigned int blockDimX, unsigned int blockDimY,
                        unsigned int blockDimZ, unsigned int sharedMemBytes,
                        CUstream hStream, void** kernelParams,
                        void** extra);
CUresult cuCtxSynchronize(void);
CUresult cuGetErrorString(CUresult error, const char** pStr);
CUresult cuGetErrorName(CUresult error, const char** pStr);

/* Plain aliases (ABI-identical to the _v2 forms), exported as well so the
 * API surface mirrors a real driver where both symbol names exist. */
CUresult cuCtxCreate(CUcontext* pctx, unsigned int flags, CUdevice device);
CUresult cuCtxDestroy(CUcontext ctx);
CUresult cuModuleGetGlobal(CUdeviceptr* dptr, size_t* bytes, CUmodule hmod,
                           const char* name);
CUresult cuMemAlloc(CUdeviceptr* dptr, size_t bytes);
CUresult cuMemcpyHtoD(CUdeviceptr dstDevice, const void* srcHost,
                      size_t ByteCount);
CUresult cuMemcpyDtoH(void* dstHost, CUdeviceptr srcDevice, size_t ByteCount);
CUresult cuMemsetD8(CUdeviceptr dstDevice, unsigned char uc, size_t ByteCount);

#ifdef __cplusplus
}
#endif

#endif /* FAKE_CUDA_H */