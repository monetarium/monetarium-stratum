#ifndef _POSIX_C_SOURCE
#define _POSIX_C_SOURCE 200809L
#endif

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "fake_cuda.h"
#include "cpu_reference.h"

/* ------------------------------------------------------------------ */
/* Error strings (covers the errors host.cpp inspects).                */
/* ------------------------------------------------------------------ */

static const char* error_str(int code) {
    switch (code) {
        case 0:   return "CUDA_SUCCESS";
        case 1:   return "CUDA_ERROR_INVALID_VALUE";
        case 2:   return "CUDA_ERROR_OUT_OF_MEMORY";
        case 3:   return "CUDA_ERROR_NOT_INITIALIZED";
        case 100: return "CUDA_ERROR_NO_DEVICE";
        case 101: return "CUDA_ERROR_INVALID_DEVICE";
        case 201: return "CUDA_ERROR_INVALID_CONTEXT";
        case 400: return "CUDA_ERROR_INVALID_HANDLE";
        case 500: return "CUDA_ERROR_NOT_FOUND";
        case 502: return "CUDA_ERROR_INVALID_IMAGE";
        case 503: return "CUDA_ERROR_INVALID_SOURCE";
        default:  return "CUDA_ERROR_UNKNOWN";
    }
}

/* ------------------------------------------------------------------ */
/* Fail-next hooks                                                     */
/* ------------------------------------------------------------------ */

static CUresult   fail_cuda[FAKE_OP_COUNT];

static const char* fake_op_name(FakeCudaOperation op) {
    switch (op) {
        case FAKE_OP_INIT:              return "init";
        case FAKE_OP_DRIVER_VERSION:    return "driver_version";
        case FAKE_OP_DEVICE_GET_COUNT:  return "device_get_count";
        case FAKE_OP_DEVICE_GET:        return "device_get";
        case FAKE_OP_DEVICE_GET_NAME:   return "device_get_name";
        case FAKE_OP_DEVICE_GET_ATTRIBUTE: return "device_get_attribute";
        case FAKE_OP_CTX_CREATE:        return "ctx_create";
        case FAKE_OP_CTX_DESTROY:       return "ctx_destroy";
        case FAKE_OP_MODULE_LOAD:       return "module_load";
        case FAKE_OP_MODULE_GET_FUNCTION: return "module_get_function";
        case FAKE_OP_MODULE_GET_GLOBAL: return "module_get_global";
        case FAKE_OP_MODULE_UNLOAD:     return "module_unload";
        case FAKE_OP_MEM_ALLOC:         return "mem_alloc";
        case FAKE_OP_MEM_HTOD:          return "mem_htod";
        case FAKE_OP_MEM_DTOH:          return "mem_dtoh";
        case FAKE_OP_MEM_MEMSET:        return "mem_memset";
        case FAKE_OP_MEM_FREE:          return "mem_free";
        case FAKE_OP_LAUNCH:            return "launch";
        case FAKE_OP_SYNC:              return "sync";
        case FAKE_OP_GET_ERROR_STRING:  return "get_error_string";
        case FAKE_OP_GET_ERROR_NAME:    return "get_error_name";
        default:                        return "?";
    }
}

/*
 * One-shot injection via FAKE_CUDA_FAIL_NEXT=op:err, read from the process
 * environment.  This lets the Go integration tests drive the unmodified
 * cuda_host binary.  The variable is consumed only when it names the operation
 * currently being called, so it can be aimed at a specific interaction.
 */
static CUresult env_fail(FakeCudaOperation op) {
    const char* v = getenv("FAKE_CUDA_FAIL_NEXT");
    if (!v || !*v) return CUDA_SUCCESS;
    char opname[64] = {0};
    int err = 0;
    if (sscanf(v, "%63[^:]:%d", opname, &err) != 2) return CUDA_SUCCESS;
    if (strcmp(opname, fake_op_name(op)) != 0) return CUDA_SUCCESS;
    unsetenv("FAKE_CUDA_FAIL_NEXT");
    return (CUresult)err;
}

static CUresult check_fail(FakeCudaOperation op) {
    CUresult err = fail_cuda[op];
    if (err) fail_cuda[op] = 0;
    else err = env_fail(op);
    return err;
}

void fake_cuda_fail_next(FakeCudaOperation op, CUresult err) {
    fail_cuda[op] = err;
}

/* ------------------------------------------------------------------ */
/* Fake device configuration                                           */
/* ------------------------------------------------------------------ */

static int dev_major = 8, dev_minor = 9, driver_version = 13020;

void fake_cuda_set_compute_capability(int major, int minor) {
    dev_major = major;
    dev_minor = minor;
}

void fake_cuda_set_driver_version(int version) {
    driver_version = version;
}

/* ------------------------------------------------------------------ */
/* Device memory registry                                              */
/* ------------------------------------------------------------------ */

struct alloc {
    CUdeviceptr addr;
    void*      ptr;
    size_t     size;
    int        alive;
};

#define MAX_ALLOCS 256
static struct alloc allocs[MAX_ALLOCS];
static int          alloc_count;
static int          alloc_total;

static struct alloc* find_alloc(CUdeviceptr addr) {
    for (int i = 0; i < alloc_count; i++)
        if (allocs[i].addr == addr && allocs[i].alive) return &allocs[i];
    return NULL;
}

/* ------------------------------------------------------------------ */
/* Module registry                                                     */
/* ------------------------------------------------------------------ */

struct module_const {
    char     name[32];
    struct alloc* alloc;
};

struct module_fn {
    char name[64];
    int  is_kernel;
};

struct module {
    int             id;
    struct alloc    cv;
    struct alloc    block2;
    struct alloc    target;
    struct module_fn fns[16];
    int             fn_count;
    struct module_const consts[16];
    int             const_count;
    int             alive;
};

#define MAX_MODULES 32
static struct module modules[MAX_MODULES];
static int          module_count;

static struct module* find_module(int id) {
    for (int i = 0; i < module_count; i++)
        if (modules[i].id == id && modules[i].alive) return &modules[i];
    return NULL;
}

/* Unified device-address resolver: plain allocations AND module constants. */
static struct alloc* find_dev_ptr(CUdeviceptr addr) {
    struct alloc* a = find_alloc(addr);
    if (a) return a;
    for (int i = 0; i < module_count; i++) {
        struct module* m = &modules[i];
        if (!m->alive) continue;
        if (m->cv.addr == addr) return &m->cv;
        if (m->block2.addr == addr) return &m->block2;
        if (m->target.addr == addr) return &m->target;
    }
    return NULL;
}

/* ------------------------------------------------------------------ */
/* Context registry                                                    */
/* ------------------------------------------------------------------ */

struct context {
    int id;
    int alive;
};

#define MAX_CONTEXTS 64
static struct context contexts[MAX_CONTEXTS];
static int           context_count;

static int current_ctx_id = -1;

static struct context* find_ctx(int id) {
    for (int i = 0; i < context_count; i++)
        if (contexts[i].id == id && contexts[i].alive) return &contexts[i];
    return NULL;
}

/* ------------------------------------------------------------------ */
/* Launch config capture                                                */
/* ------------------------------------------------------------------ */

static struct {
    unsigned int gx, gy, gz;
    unsigned int bx, by, bz;
    unsigned int smem;
} last_launch;

int fake_cuda_get_last_launch(unsigned int* gx, unsigned int* gy,
                              unsigned int* gz, unsigned int* bx,
                              unsigned int* by, unsigned int* bz,
                              unsigned int* smem) {
    if (gx) *gx = last_launch.gx;
    if (gy) *gy = last_launch.gy;
    if (gz) *gz = last_launch.gz;
    if (bx) *bx = last_launch.bx;
    if (by) *by = last_launch.by;
    if (bz) *bz = last_launch.bz;
    if (smem) *smem = last_launch.smem;
    return 0;
}

/* ------------------------------------------------------------------ */
/* State reset                                                         */
/* ------------------------------------------------------------------ */

void fake_cuda_reset(void) {
    for (int i = 0; i < alloc_count; i++)
        free(allocs[i].ptr);
    for (int i = 0; i < module_count; i++) {
        if (!modules[i].alive) continue;
        free(modules[i].cv.ptr);
        free(modules[i].block2.ptr);
        free(modules[i].target.ptr);
    }
    memset(fail_cuda, 0, sizeof(fail_cuda));
    alloc_count = 0;
    alloc_total = 0;
    module_count = 0;
    context_count = 0;
    current_ctx_id = -1;
    memset(&last_launch, 0, sizeof(last_launch));
    dev_major = 8; dev_minor = 9;
    driver_version = 13020;
}

/* ------------------------------------------------------------------ */
/* Launch config capture                                                */
/* ------------------------------------------------------------------ */

/* ------------------------------------------------------------------ */
/* Leak accounting helpers                                             */
/* ------------------------------------------------------------------ */

int fake_cuda_live_contexts(void) {
    int n = 0;
    for (int i = 0; i < context_count; i++) n += contexts[i].alive;
    return n;
}

int fake_cuda_live_allocations(void) {
    int n = 0;
    for (int i = 0; i < alloc_count; i++) n += allocs[i].alive;
    return n;
}

int fake_cuda_live_modules(void) {
    int n = 0;
    for (int i = 0; i < module_count; i++) n += modules[i].alive;
    return n;
}

int fake_cuda_live_constants(void) {
    int n = 0;
    for (int i = 0; i < module_count; i++) {
        if (!modules[i].alive) continue;
        n += modules[i].const_count;
    }
    return n;
}

int fake_cuda_total_allocs(void) { return alloc_total; }

int fake_cuda_assert_no_leaks(void) {
    int ok = 1;
    for (int i = 0; i < alloc_count; i++) {
        if (allocs[i].alive) {
            fprintf(stderr, "LEAK: device ptr %p size %zu alive\n",
                    (void*)(uintptr_t)allocs[i].addr, allocs[i].size);
            ok = 0;
        }
    }
    return ok ? 0 : -1;
}

int fake_cuda_ptr_info(CUdeviceptr addr, void** base, size_t* size,
                       int* is_const) {
    for (int i = 0; i < module_count; i++) {
        struct module* m = &modules[i];
        if (!m->alive) continue;
        if (m->cv.addr == addr) {
            if (base)  *base = m->cv.ptr;
            if (size)  *size = m->cv.size;
            if (is_const) *is_const = 1;
            return 0;
        }
        if (m->block2.addr == addr) {
            if (base)  *base = m->block2.ptr;
            if (size)  *size = m->block2.size;
            if (is_const) *is_const = 1;
            return 0;
        }
        if (m->target.addr == addr) {
            if (base)  *base = m->target.ptr;
            if (size)  *size = m->target.size;
            if (is_const) *is_const = 1;
            return 0;
        }
    }
    for (int i = 0; i < alloc_count; i++) {
        if (allocs[i].addr == addr && allocs[i].alive) {
            if (base)  *base = allocs[i].ptr;
            if (size)  *size = allocs[i].size;
            if (is_const) *is_const = 0;
            return 0;
        }
    }
    return -1;
}

/* ------------------------------------------------------------------ */
/* Launch config capture (called from the CPU-reference path).         */
/* ------------------------------------------------------------------ */

static void capture_launch(unsigned int gx, unsigned int gy, unsigned int gz,
                           unsigned int bx, unsigned int by, unsigned int bz,
                           unsigned int smem) {
    last_launch.gx   = gx;
    last_launch.gy   = gy;
    last_launch.gz   = gz;
    last_launch.bx   = bx;
    last_launch.by   = by;
    last_launch.bz   = bz;
    last_launch.smem = smem;
}

/* ------------------------------------------------------------------ */
/* CUDA Driver API implementation                                      */
/* ------------------------------------------------------------------ */

static int context_id_counter;

CUresult cuInit(unsigned int flags) {
    (void)flags;
    CUresult r = check_fail(FAKE_OP_INIT);
    if (r) return r;
    return CUDA_SUCCESS;
}

CUresult cuDriverGetVersion(int* version) {
    CUresult r = check_fail(FAKE_OP_DRIVER_VERSION);
    if (r) return r;
    if (!version) return CUDA_ERROR_INVALID_VALUE;
    *version = driver_version;
    return CUDA_SUCCESS;
}

CUresult cuDeviceGetCount(int* count) {
    CUresult r = check_fail(FAKE_OP_DEVICE_GET_COUNT);
    if (r) return r;
    if (!count) return CUDA_ERROR_INVALID_VALUE;
    *count = 1;
    return CUDA_SUCCESS;
}

CUresult cuDeviceGet(CUdevice* device, int ordinal) {
    CUresult r = check_fail(FAKE_OP_DEVICE_GET);
    if (r) return r;
    if (!device) return CUDA_ERROR_INVALID_VALUE;
    if (ordinal != 0) return CUDA_ERROR_INVALID_DEVICE;
    *device = 0;
    return CUDA_SUCCESS;
}

CUresult cuDeviceGetName(char* name, int len, CUdevice device) {
    CUresult r = check_fail(FAKE_OP_DEVICE_GET_NAME);
    if (r) return r;
    if (!name || len <= 0) return CUDA_ERROR_INVALID_VALUE;
    if (device != 0) return CUDA_ERROR_INVALID_DEVICE;
    const char* src = "CPU CUDA Test Device";
    strncpy(name, src, (size_t)(len - 1));
    name[len - 1] = '\0';
    return CUDA_SUCCESS;
}

CUresult cuDeviceGetAttribute(int* pi, int attrib, CUdevice device) {
    CUresult r = check_fail(FAKE_OP_DEVICE_GET_ATTRIBUTE);
    if (r) return r;
    if (!pi) return CUDA_ERROR_INVALID_VALUE;
    if (device != 0) return CUDA_ERROR_INVALID_DEVICE;
    switch (attrib) {
        case CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MAJOR:
            *pi = dev_major;
            return CUDA_SUCCESS;
        case CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MINOR:
            *pi = dev_minor;
            return CUDA_SUCCESS;
        default:
            return CUDA_ERROR_INVALID_VALUE;
    }
}

CUresult cuCtxCreate_v2(CUcontext* pctx, unsigned int flags,
                        CUdevice device) {
    (void)flags;
    CUresult r = check_fail(FAKE_OP_CTX_CREATE);
    if (r) return r;
    if (!pctx) return CUDA_ERROR_INVALID_VALUE;
    if (device != 0) return CUDA_ERROR_INVALID_DEVICE;
    if (context_count >= MAX_CONTEXTS) return CUDA_ERROR_OUT_OF_MEMORY;
    int id = ++context_id_counter;
    contexts[context_count].id    = id;
    contexts[context_count].alive = 1;
    context_count++;
    current_ctx_id = id;
    *pctx = (CUcontext)(uintptr_t)id;
    return CUDA_SUCCESS;
}

CUresult cuCtxDestroy_v2(CUcontext ctx) {
    CUresult r = check_fail(FAKE_OP_CTX_DESTROY);
    if (r) return r;
    int id = (int)(uintptr_t)ctx;
    struct context* c = find_ctx(id);
    if (!c) return CUDA_ERROR_INVALID_CONTEXT;
    c->alive = 0;
    if (current_ctx_id == id) current_ctx_id = -1;
    return CUDA_SUCCESS;
}

static void init_module_constants(struct module* m) {
    m->const_count = 0;
    int id = m->id;

    struct alloc* ca = &m->cv;
    ca->addr  = (CUdeviceptr)((uintptr_t)0x100000000ULL + (uintptr_t)id * 0x1000);
    ca->size  = 32;
    ca->ptr   = calloc(1, 32);
    ca->alive = 1;

    struct alloc* ba = &m->block2;
    ba->addr  = ca->addr + 0x1000;
    ba->size  = 64;
    ba->ptr   = calloc(1, 64);
    ba->alive = 1;

    struct alloc* ta = &m->target;
    ta->addr  = ba->addr + 0x1000;
    ta->size  = 32;
    ta->ptr   = calloc(1, 32);
    ta->alive = 1;

    int n = 0;
    strncpy(m->consts[n].name, "d_cv", 32);
    m->consts[n].alloc = ca; n++;
    strncpy(m->consts[n].name, "d_block2", 32);
    m->consts[n].alloc = ba; n++;
    strncpy(m->consts[n].name, "d_target", 32);
    m->consts[n].alloc = ta; n++;
    m->const_count = n;

    m->fn_count = 0;
    strncpy(m->fns[m->fn_count].name, "search_nonce", 64);
    m->fns[m->fn_count].is_kernel = 1;
    m->fn_count++;
}

CUresult cuModuleLoadDataEx(CUmodule* module, const void* image,
                            unsigned int numOptions, int* options,
                            void** optionValues) {
    (void)image; (void)numOptions; (void)options; (void)optionValues;
    CUresult r = check_fail(FAKE_OP_MODULE_LOAD);
    if (r) return r;
    if (!module) return CUDA_ERROR_INVALID_VALUE;
    if (module_count >= MAX_MODULES) return CUDA_ERROR_OUT_OF_MEMORY;
    struct module* m = &modules[module_count];
    m->id     = module_count + 1;
    m->alive  = 1;
    init_module_constants(m);
    module_count++;
    *module = (CUmodule)(uintptr_t)m->id;
    return CUDA_SUCCESS;
}

CUresult cuModuleGetFunction(CUfunction* func, CUmodule hmod,
                             const char* name) {
    CUresult r = check_fail(FAKE_OP_MODULE_GET_FUNCTION);
    if (r) return r;
    if (!func || !name) return CUDA_ERROR_INVALID_VALUE;
    struct module* m = find_module((int)(uintptr_t)hmod);
    if (!m) return CUDA_ERROR_INVALID_HANDLE;
    for (int i = 0; i < m->fn_count; i++) {
        if (strcmp(m->fns[i].name, name) == 0) {
            *func = (CUfunction)(uintptr_t)(m->id << 16 | i);
            return CUDA_SUCCESS;
        }
    }
    return CUDA_ERROR_NOT_FOUND;
}

CUresult cuModuleGetGlobal_v2(CUdeviceptr* dptr, size_t* bytes,
                              CUmodule hmod, const char* name) {
    CUresult r = check_fail(FAKE_OP_MODULE_GET_GLOBAL);
    if (r) return r;
    if (!dptr || !name) return CUDA_ERROR_INVALID_VALUE;
    struct module* m = find_module((int)(uintptr_t)hmod);
    if (!m) return CUDA_ERROR_INVALID_HANDLE;
    for (int i = 0; i < m->const_count; i++) {
        if (strcmp(m->consts[i].name, name) == 0) {
            *dptr = m->consts[i].alloc->addr;
            if (bytes) *bytes = m->consts[i].alloc->size;
            return CUDA_SUCCESS;
        }
    }
    return CUDA_ERROR_NOT_FOUND;
}

CUresult cuModuleUnload(CUmodule hmod) {
    CUresult r = check_fail(FAKE_OP_MODULE_UNLOAD);
    if (r) return r;
    struct module* m = find_module((int)(uintptr_t)hmod);
    if (!m) return CUDA_ERROR_INVALID_HANDLE;
    free(m->cv.ptr);
    free(m->block2.ptr);
    free(m->target.ptr);
    m->cv.addr = 0; m->cv.ptr = NULL; m->cv.alive = 0;
    m->block2.addr = 0; m->block2.ptr = NULL; m->block2.alive = 0;
    m->target.addr = 0; m->target.ptr = NULL; m->target.alive = 0;
    m->alive = 0;
    return CUDA_SUCCESS;
}

CUresult cuMemAlloc_v2(CUdeviceptr* dptr, size_t bytes) {
    CUresult r = check_fail(FAKE_OP_MEM_ALLOC);
    if (r) return r;
    if (!dptr || bytes == 0) return CUDA_ERROR_INVALID_VALUE;
    if (alloc_count >= MAX_ALLOCS) return CUDA_ERROR_OUT_OF_MEMORY;
    void* p = calloc(1, bytes ? bytes : 1);
    if (!p) return CUDA_ERROR_OUT_OF_MEMORY;
    CUdeviceptr addr = (CUdeviceptr)(uintptr_t)p;
    allocs[alloc_count].addr  = addr;
    allocs[alloc_count].ptr   = p;
    allocs[alloc_count].size  = bytes;
    allocs[alloc_count].alive = 1;
    alloc_count++;
    alloc_total++;
    *dptr = addr;
    return CUDA_SUCCESS;
}

CUresult cuMemFree(CUdeviceptr dptr) {
    CUresult r = check_fail(FAKE_OP_MEM_FREE);
    if (r) return r;
    struct alloc* a = find_alloc(dptr);
    if (!a) return CUDA_ERROR_INVALID_HANDLE;
    a->alive = 0;
    return CUDA_SUCCESS;
}

CUresult cuMemcpyHtoD_v2(CUdeviceptr dst, const void* src, size_t len) {
    CUresult r = check_fail(FAKE_OP_MEM_HTOD);
    if (r) return r;
    struct alloc* a = find_dev_ptr(dst);
    if (!a) return CUDA_ERROR_INVALID_HANDLE;
    if (a->size < len) return CUDA_ERROR_INVALID_VALUE;
    memcpy(a->ptr, src, len);
    return CUDA_SUCCESS;
}

CUresult cuMemcpyDtoH_v2(void* dst, CUdeviceptr src, size_t len) {
    CUresult r = check_fail(FAKE_OP_MEM_DTOH);
    if (r) return r;
    struct alloc* a = find_dev_ptr(src);
    if (!a) return CUDA_ERROR_INVALID_HANDLE;
    if (a->size < len) return CUDA_ERROR_INVALID_VALUE;
    memcpy(dst, a->ptr, len);
    return CUDA_SUCCESS;
}

CUresult cuMemsetD8_v2(CUdeviceptr dst, unsigned char uc, size_t len) {
    CUresult r = check_fail(FAKE_OP_MEM_MEMSET);
    if (r) return r;
    struct alloc* a = find_dev_ptr(dst);
    if (!a) return CUDA_ERROR_INVALID_HANDLE;
    if (a->size < len) return CUDA_ERROR_INVALID_VALUE;
    memset(a->ptr, uc, len);
    return CUDA_SUCCESS;
}

/*
 * Distinguish the function handle for search_nonce from unknown functions.
 * A real kernel handle is encoded as (module_id << 16 | fn_index).
 */
static struct module* mod_from_handle(CUfunction f, int* fn_idx) {
    int raw = (int)(uintptr_t)f;
    int mid = raw >> 16;
    int idx = raw & 0xFFFF;
    struct module* m = find_module(mid);
    if (!m) return NULL;
    if (idx < 0 || idx >= m->fn_count) return NULL;
    if (fn_idx) *fn_idx = idx;
    return m;
}

CUresult cuLaunchKernel(CUfunction f, unsigned int gx, unsigned int gy,
                        unsigned int gz, unsigned int bx, unsigned int by,
                        unsigned int bz, unsigned int smem, CUstream stream,
                        void** kernelParams, void** extra) {
    (void)stream; (void)extra;
    CUresult r = check_fail(FAKE_OP_LAUNCH);
    if (r) return r;

    int fn_idx;
    struct module* m = mod_from_handle(f, &fn_idx);
    if (!m) return CUDA_ERROR_INVALID_HANDLE;

    if (gx == 0 || gy == 0 || gz == 0 ||
        bx == 0 || by == 0 || bz == 0)
        return CUDA_ERROR_INVALID_VALUE;

    if (!kernelParams) return CUDA_ERROR_INVALID_VALUE;

    capture_launch(gx, gy, gz, bx, by, bz, smem);

    uint32_t start_nonce, nonces_to_search;
    CUdeviceptr result_dev, hash_dev;

    memcpy(&start_nonce,     kernelParams[0], sizeof(uint32_t));
    memcpy(&nonces_to_search, kernelParams[1], sizeof(uint32_t));
    memcpy(&result_dev,      kernelParams[2], sizeof(CUdeviceptr));
    memcpy(&hash_dev,        kernelParams[3], sizeof(CUdeviceptr));

    if (start_nonce == 0 || nonces_to_search == 0)
        return CUDA_ERROR_INVALID_VALUE;

    /*
     * Optional cap on the nonce range a single launch scans, set via
     * FAKE_CUDA_MAX_NONCES.  The real host sweeps 2^32 nonces in 64 launches
     * of 64M each; capping lets a no-solution integration test finish in
     * milliseconds while keeping the host's launch protocol (and its
     * nonces_checked accounting) unchanged.
     */
    const char* cap_env = getenv("FAKE_CUDA_MAX_NONCES");
    if (cap_env && *cap_env) {
        unsigned long cap = strtoul(cap_env, NULL, 10);
        if (cap > 0 && nonces_to_search > cap)
            nonces_to_search = (uint32_t)cap;
    }

    struct alloc* r_alloc = find_alloc(result_dev);
    struct alloc* h_alloc = find_alloc(hash_dev);
    if (!r_alloc || !h_alloc) return CUDA_ERROR_INVALID_VALUE;
    if (r_alloc->size < 4 || h_alloc->size < 32)
        return CUDA_ERROR_INVALID_VALUE;

    uint32_t cv[8], block2[16], target[8];
    memcpy(cv,     m->cv.ptr,     32);
    memcpy(block2, m->block2.ptr, 64);
    memcpy(target, m->target.ptr, 32);

    int32_t found = 0;
    uint32_t hash_out[8] = {0};
    cpu_search_nonce(cv, block2, target, start_nonce, nonces_to_search,
                     &found, hash_out);

    memset(r_alloc->ptr, 0, 4);
    memset(h_alloc->ptr, 0, 32);
    if (found) {
        memcpy(r_alloc->ptr, &found, 4);
        memcpy(h_alloc->ptr, hash_out, 32);
    }

    return CUDA_SUCCESS;
}

CUresult cuCtxSynchronize(void) {
    CUresult r = check_fail(FAKE_OP_SYNC);
    if (r) return r;
    return CUDA_SUCCESS;
}

CUresult cuGetErrorString(CUresult error, const char** pStr) {
    CUresult r = check_fail(FAKE_OP_GET_ERROR_STRING);
    if (r) { if (pStr) *pStr = ""; return r; }
    if (!pStr) return CUDA_ERROR_INVALID_VALUE;
    *pStr = error_str((int)error);
    return CUDA_SUCCESS;
}

CUresult cuGetErrorName(CUresult error, const char** pStr) {
    CUresult r = check_fail(FAKE_OP_GET_ERROR_NAME);
    if (r) { if (pStr) *pStr = ""; return r; }
    if (!pStr) return CUDA_ERROR_INVALID_VALUE;
    *pStr = error_str((int)error);
    return CUDA_SUCCESS;
}

/* _v2 aliases — the host's cuSym() chain prefers these. */

CUresult cuCtxCreate(CUcontext* pctx, unsigned int flags,
                     CUdevice device)
    __attribute__((alias("cuCtxCreate_v2")));
CUresult cuCtxDestroy(CUcontext ctx)
    __attribute__((alias("cuCtxDestroy_v2")));
CUresult cuModuleGetGlobal(CUdeviceptr* dptr, size_t* bytes,
                           CUmodule hmod, const char* name)
    __attribute__((alias("cuModuleGetGlobal_v2")));
CUresult cuMemAlloc(CUdeviceptr* dptr, size_t bytes)
    __attribute__((alias("cuMemAlloc_v2")));
CUresult cuMemcpyHtoD(CUdeviceptr dst, const void* src, size_t len)
    __attribute__((alias("cuMemcpyHtoD_v2")));
CUresult cuMemcpyDtoH(void* dst, CUdeviceptr src, size_t len)
    __attribute__((alias("cuMemcpyDtoH_v2")));
CUresult cuMemsetD8(CUdeviceptr dst, unsigned char uc, size_t len)
    __attribute__((alias("cuMemsetD8_v2")));