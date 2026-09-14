#ifndef CU_H
#define CU_H

#include <stddef.h>
#include <stdint.h>

typedef int32_t CUresult;
typedef int32_t CUdevice;
typedef uintptr_t CUcontext;
typedef uintptr_t CUfunction;
typedef uintptr_t CUmodule;
typedef uintptr_t CUstream;
typedef uint64_t CUdeviceptr;

#define CUDA_SUCCESS                      0
#define CUDA_ERROR_INVALID_VALUE          1
#define CUDA_ERROR_OUT_OF_MEMORY          2
#define CUDA_ERROR_NOT_INITIALIZED        3
#define CUDA_ERROR_NO_DEVICE              100
#define CUDA_ERROR_INVALID_DEVICE         101
#define CUDA_ERROR_INVALID_CONTEXT        201
#define CUDA_ERROR_INVALID_HANDLE         400
#define CUDA_ERROR_NOT_FOUND              500
#define CUDA_ERROR_INVALID_IMAGE          502
#define CUDA_ERROR_INVALID_SOURCE         503

#define CU_CTX_SCHED_AUTO 0x00

#define CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MAJOR  75
#define CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MINOR  76

typedef CUresult (*pfn_cuInit)(unsigned int);
typedef CUresult (*pfn_cuDriverGetVersion)(int*);
typedef CUresult (*pfn_cuDeviceGetCount)(int*);
typedef CUresult (*pfn_cuDeviceGet)(CUdevice*, int);
typedef CUresult (*pfn_cuDeviceGetName)(char*, int, CUdevice);
typedef CUresult (*pfn_cuDeviceGetAttribute)(int*, int, CUdevice);
typedef CUresult (*pfn_cuCtxCreate_v2)(CUcontext*, unsigned int, CUdevice);
typedef CUresult (*pfn_cuCtxDestroy_v2)(CUcontext);
typedef CUresult (*pfn_cuModuleLoadDataEx)(CUmodule*, const void*, unsigned int, int*, void**);
typedef CUresult (*pfn_cuModuleGetFunction)(CUfunction*, CUmodule, const char*);
typedef CUresult (*pfn_cuModuleGetGlobal)(CUdeviceptr*, size_t*, CUmodule, const char*);
typedef CUresult (*pfn_cuModuleUnload)(CUmodule);
typedef CUresult (*pfn_cuMemAlloc)(CUdeviceptr*, size_t);
typedef CUresult (*pfn_cuMemcpyHtoD)(CUdeviceptr, const void*, size_t);
typedef CUresult (*pfn_cuMemcpyDtoH)(void*, CUdeviceptr, size_t);
typedef CUresult (*pfn_cuMemsetD8)(CUdeviceptr, unsigned char, size_t);
typedef CUresult (*pfn_cuLaunchKernel)(CUfunction,
                                       unsigned int, unsigned int, unsigned int,
                                       unsigned int, unsigned int, unsigned int,
                                       unsigned int, CUstream, void**, void**);
typedef CUresult (*pfn_cuCtxSynchronize)(void);
typedef CUresult (*pfn_cuGetErrorString)(CUresult, const char**);
typedef CUresult (*pfn_cuGetErrorName)(CUresult, const char**);

typedef void* nvrtcProgram;
typedef int32_t nvrtcResult;

#define NVRTC_SUCCESS 0
#define NVRTC_ERROR_COMPILATION 6

typedef nvrtcResult (*pfn_nvrtcCreateProgram)(nvrtcProgram*, const char*, const char*,
                                              int, const char**, const char**);
typedef nvrtcResult (*pfn_nvrtcAddNameExpression)(nvrtcProgram, const char*);
typedef nvrtcResult (*pfn_nvrtcGetLoweredName)(nvrtcProgram, const char*, const char**);
typedef nvrtcResult (*pfn_nvrtcCompileProgram)(nvrtcProgram, int, const char**);
typedef nvrtcResult (*pfn_nvrtcGetPTXSize)(nvrtcProgram, size_t*);
typedef nvrtcResult (*pfn_nvrtcGetPTX)(nvrtcProgram, char*);
typedef nvrtcResult (*pfn_nvrtcGetProgramLogSize)(nvrtcProgram, size_t*);
typedef nvrtcResult (*pfn_nvrtcGetProgramLog)(nvrtcProgram, char*);
typedef nvrtcResult (*pfn_nvrtcDestroyProgram)(nvrtcProgram*);
typedef const char* (*pfn_nvrtcGetErrorString)(nvrtcResult);

#endif