#ifndef CL_H
#define CL_H

#include <stddef.h>
#include <stdint.h>

typedef int32_t  cl_int;
typedef uint32_t cl_uint;
typedef uint64_t cl_ulong;
typedef uintptr_t cl_platform_id;
typedef uintptr_t cl_device_id;
typedef uintptr_t cl_context;
typedef uintptr_t cl_command_queue;
typedef uintptr_t cl_mem;
typedef uintptr_t cl_program;
typedef uintptr_t cl_kernel;
typedef uintptr_t cl_event;
typedef uint32_t cl_bool;

#define CL_DEVICE_TYPE_GPU         0x1
#define CL_DEVICE_TYPE_CPU         0x2
#define CL_MEM_READ_ONLY           (1U<<0)
#define CL_MEM_WRITE_ONLY          (1U<<1)
#define CL_MEM_READ_WRITE          (1U<<2)
#define CL_MEM_COPY_HOST_PTR       (1U<<3)
#define CL_QUEUE_PROFILING_ENABLE  (1U<<2)
#define CL_PROGRAM_BUILD_LOG       0x1183
#define CL_PROGRAM_BUILD_STATUS    0x1184
#define CL_PLATFORM_NAME           0x0902
#define CL_DEVICE_NAME             0x102b
#define CL_DEVICE_TYPE             0x1000
#define CL_TRUE  1
#define CL_FALSE 0
#define CL_SUCCESS                0
#define CL_DEVICE_NOT_FOUND       -1
#define CL_DEVICE_NOT_AVAILABLE   -2
#define CL_COMPILER_NOT_AVAILABLE -3
#define CL_MEM_OBJECT_ALLOCATION_FAILURE -4
#define CL_OUT_OF_RESOURCES       -5
#define CL_OUT_OF_HOST_MEMORY     -6
#define CL_INVALID_PROGRAM        -7
#define CL_INVALID_KERNEL         -8
#define CL_INVALID_ARG_INDEX      -9
#define CL_INVALID_ARG_VALUE      -10
#define CL_INVALID_MEM_OBJECT     -11
#define CL_INVALID_SAMPLER        -12
#define CL_INVALID_BINARY         -13
#define CL_INVALID_BUILD_OPTIONS  -14
#define CL_BUILD_PROGRAM_FAILURE  -15
#define CL_INVALID_COMMAND_QUEUE  -16
#define CL_INVALID_WORK_GROUP_SIZE -17

typedef cl_int (*pfn_clGetPlatformIDs)(cl_uint, cl_platform_id*, cl_uint*);
typedef cl_int (*pfn_clGetPlatformInfo)(cl_platform_id, cl_uint, size_t, void*, size_t*);
typedef cl_int (*pfn_clGetDeviceIDs)(cl_platform_id, cl_ulong, cl_uint, cl_device_id*, cl_uint*);
typedef cl_int (*pfn_clGetDeviceInfo)(cl_device_id, cl_uint, size_t, void*, size_t*);
typedef cl_context (*pfn_clCreateContext)(const void*, cl_uint, const cl_device_id*, void*, void*, cl_int*);
typedef cl_command_queue (*pfn_clCreateCommandQueue)(cl_context, cl_device_id, cl_uint, cl_int*);
typedef cl_mem (*pfn_clCreateBuffer)(cl_context, cl_ulong, size_t, void*, cl_int*);
typedef cl_program (*pfn_clCreateProgramWithSource)(cl_context, cl_uint, const char**, const size_t*, cl_int*);
typedef cl_int (*pfn_clBuildProgram)(cl_program, cl_uint, const cl_device_id*, const char*, void*, void*);
typedef cl_kernel (*pfn_clCreateKernel)(cl_program, const char*, cl_int*);
typedef cl_int (*pfn_clSetKernelArg)(cl_kernel, cl_uint, size_t, const void*);
typedef cl_int (*pfn_clEnqueueNDRangeKernel)(cl_command_queue, cl_kernel, cl_uint, const size_t*, const size_t*, const size_t*, cl_uint, const cl_event*, cl_event*);
typedef cl_int (*pfn_clWaitForEvents)(cl_uint, const cl_event*);
typedef cl_int (*pfn_clReleaseEvent)(cl_event);
typedef cl_int (*pfn_clEnqueueReadBuffer)(cl_command_queue, cl_mem, cl_bool, size_t, size_t, void*, cl_uint, const cl_event*, cl_event*);
typedef cl_int (*pfn_clEnqueueFillBuffer)(cl_command_queue, cl_mem, const void*, size_t, size_t, size_t, cl_uint, const cl_event*, cl_event*);
typedef cl_int (*pfn_clReleaseMemObject)(cl_mem);
typedef cl_int (*pfn_clReleaseKernel)(cl_kernel);
typedef cl_int (*pfn_clReleaseProgram)(cl_program);
typedef cl_int (*pfn_clReleaseCommandQueue)(cl_command_queue);
typedef cl_int (*pfn_clReleaseContext)(cl_context);
typedef cl_int (*pfn_clGetProgramBuildInfo)(cl_program, cl_device_id, cl_uint, size_t, void*, size_t*);

#endif