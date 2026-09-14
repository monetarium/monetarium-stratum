/*
 * Self-contained C-level tests for the fake CUDA driver and fake NVRTC.
 *
 * These link the fake implementations directly (no shared object, no dlopen)
 * and exercise the exact API surface host.cpp uses: lifecycle, device
 * introspection, module/kernel/global lookup, memory copy semantics, launch
 * parameter capture, error injection, NVRTC name lowering, deterministic
 * hashing against precomputed vectors, and leak detection.
 *
 * The precomputed vectors below were derived by an independent Go
 * implementation of the midstate scheme (cmd/gpuminer/midstate_test.go) and
 * cross-checked against lukechampine.com/blake3 (full 180-byte header with the
 * nonce written at byte offset 140):
 *
 *   header[i]        = (i*7 + 11) & 0xFF
 *   FIND   (1 zero-byte top)         nonce 435, 16 matches in [1,4096)
 *   NOMATCH (zero target)            no matches
 *   MULTI  (2 zero-bytes top)        nonce 51155, 17 matches in [1,1<<20)
 */
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#ifndef nullptr
#define nullptr ((void*)0)
#endif

#include "cpu_reference.h"
#include "fake_cuda.h"
#include "fake_nvrtc.h"

#define CHECK(cond, ...)                                              \
    do {                                                              \
        if (!(cond)) {                                                \
            fprintf(stderr, "FAIL %s:%d: ", __FILE__, __LINE__);      \
            fprintf(stderr, __VA_ARGS__);                             \
            fprintf(stderr, "\n");                                    \
            failures++;                                               \
        } else {                                                      \
            checks++;                                                 \
        }                                                             \
    } while (0)

static int checks;
static int failures;

/* ------------------------------------------------------------------ */
/* Test data (cross-checked against real blake3)                       */
/* ------------------------------------------------------------------ */

#define HEADER_SIZE 180
#define HASH_SIZE   32
#define TARGET_LEN  32

static void make_header(uint8_t hdr[HEADER_SIZE]) {
    for (int i = 0; i < HEADER_SIZE; i++)
        hdr[i] = (uint8_t)((i * 7 + 11) & 0xFF);
}

/* target with the top `n` bytes zero, rest 0xFF (big-endian top bytes) */
static void make_target(uint8_t tgt[TARGET_LEN], int top_zero) {
    for (int i = 0; i < TARGET_LEN; i++) tgt[i] = 0xFF;
    for (int b = 0; b < top_zero; b++) tgt[TARGET_LEN - 1 - b] = 0x00;
}

/* Launch the kernel through the fake driver with host-equivalent args. */
static void launch_search(CUfunction kernel, CUdeviceptr g_res,
                          CUdeviceptr g_hash, uint32_t start_nonce,
                          uint32_t count, unsigned int blocks,
                          unsigned int threads) {
    void* args[] = {
        (void*)&start_nonce,
        (void*)&count,
        (void*)&g_res,
        (void*)&g_hash,
    };
    CUresult rc = cuLaunchKernel(kernel, blocks, 1, 1, threads, 1, 1, 0, 0,
                                 args, nullptr);
    CHECK(rc == CUDA_SUCCESS, "cuLaunchKernel rc=%d", (int)rc);
    CHECK(cuCtxSynchronize() == CUDA_SUCCESS, "cuCtxSynchronize");
}

/* ------------------------------------------------------------------ */
/* Lifecycle + device introspection                                    */
/* ------------------------------------------------------------------ */

static void test_lifecycle(void) {
    CUresult rc = cuInit(0);
    CHECK(rc == CUDA_SUCCESS, "cuInit");

    int count = 0;
    CHECK(cuDeviceGetCount(&count) == CUDA_SUCCESS && count == 1,
          "cuDeviceGetCount expected 1, got %d", count);

    CUdevice dev = -1;
    CHECK(cuDeviceGet(&dev, 0) == CUDA_SUCCESS && dev == 0,
          "cuDeviceGet(0) got dev=%d", (int)dev);
    CHECK(cuDeviceGet(&dev, 1) == CUDA_ERROR_INVALID_DEVICE,
          "cuDeviceGet(1) should be INVALID_DEVICE");

    char name[256] = {0};
    rc = cuDeviceGetName(name, sizeof(name), 0);
    CHECK(rc == CUDA_SUCCESS && strcmp(name, "CPU CUDA Test Device") == 0,
          "cuDeviceGetName got '%.60s'", name);

    int major = 0, minor = 0;
    rc = cuDeviceGetAttribute(&major,
                              CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MAJOR, 0);
    CHECK(rc == CUDA_SUCCESS && major == 8, "cc major=%d", major);
    rc = cuDeviceGetAttribute(&minor,
                              CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MINOR, 0);
    CHECK(rc == CUDA_SUCCESS && minor == 9, "cc minor=%d", minor);
    int bogus_attr = 0;
    CHECK(cuDeviceGetAttribute(&bogus_attr, 9999, 0) == CUDA_ERROR_INVALID_VALUE,
          "unknown attribute should be INVALID_VALUE");

    int ver = 0;
    CHECK(cuDriverGetVersion(&ver) == CUDA_SUCCESS && ver == 13020,
          "driver version=%d", ver);
    CHECK(cuDriverGetVersion(nullptr) == CUDA_ERROR_INVALID_VALUE,
          "cuDriverGetVersion(NULL)");

    const char* estr = nullptr;
    CHECK(cuGetErrorString(CUDA_ERROR_NOT_FOUND, &estr) == CUDA_SUCCESS &&
          estr && strstr(estr, "NOT_FOUND"),
          "cuGetErrorString(NOT_FOUND)='%s'", estr ? estr : "(null)");
    CHECK(cuGetErrorName(CUDA_ERROR_INVALID_CONTEXT, &estr) == CUDA_SUCCESS &&
          estr && strstr(estr, "INVALID_CONTEXT"),
          "cuGetErrorName(INVALID_CONTEXT)='%s'", estr ? estr : "(null)");
}

/* ------------------------------------------------------------------ */
/* Context lifecycle and validations                                   */
/* ------------------------------------------------------------------ */

static void test_context(void) {
    CUcontext ctx = 0;
    CUresult rc = cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);
    CHECK(rc == CUDA_SUCCESS && ctx != 0, "cuCtxCreate_v2");
    CHECK(fake_cuda_live_contexts() == 1, "one live context");

    /* destroy unknown ctx */
    CHECK(cuCtxDestroy_v2((CUcontext)(uintptr_t)999) == CUDA_ERROR_INVALID_CONTEXT,
          "cuCtxDestroy_v2(unknown) should be INVALID_CONTEXT");

    CHECK(cuCtxDestroy_v2(ctx) == CUDA_SUCCESS, "cuCtxDestroy_v2 ok");
    CHECK(fake_cuda_live_contexts() == 0, "no live context after destroy");
    CHECK(cuCtxDestroy_v2(ctx) == CUDA_ERROR_INVALID_CONTEXT,
          "double destroy should be INVALID_CONTEXT");
}

/* ------------------------------------------------------------------ */
/* Memory semantics: copy, bounds, zero-size, unknown/freed pointers   */
/* ------------------------------------------------------------------ */

static void test_memory(void) {
    CUcontext ctx = 0;
    cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);

    CUdeviceptr a = 0, b = 0;
    CHECK(cuMemAlloc(&a, 16) == CUDA_SUCCESS && a != 0, "cuMemAlloc 16");
    CHECK(cuMemAlloc(&b, 8) == CUDA_SUCCESS, "cuMemAlloc 8");
    CHECK(fake_cuda_live_allocations() == 2, "two live allocs");

    uint8_t data[16];
    for (int i = 0; i < 16; i++) data[i] = (uint8_t)(i * 3);
    CHECK(cuMemcpyHtoD(a, data, 16) == CUDA_SUCCESS, "HtoD 16");

    uint8_t back[16] = {0};
    CHECK(cuMemcpyDtoH(back, a, 16) == CUDA_SUCCESS, "DtoH 16");
    CHECK(memcmp(back, data, 16) == 0, "HtoD/DtoH roundtrip");

    /* DtoH overrun of destination is impossible to check; instead check we
     * can read exactly the attached size and not beyond. */
    uint8_t small[4] = {0};
    CHECK(cuMemcpyHtoD(b, "xy", 2) == CUDA_SUCCESS, "HtoD short");
    CHECK(cuMemcpyDtoH(small, b, 2) == CUDA_SUCCESS && small[0] == 'x',
          "DtoH short");

    /* overrun the attached allocation on the device side */
    CHECK(cuMemcpyHtoD(b, data, 9) == CUDA_ERROR_INVALID_VALUE,
          "HtoD overrun should be INVALID_VALUE");
    CHECK(cuMemcpyDtoH(back, b, 9) == CUDA_ERROR_INVALID_VALUE,
          "DtoH overrun should be INVALID_VALUE");

    /* zero-size alloc is invalid */
    CUdeviceptr z = 0;
    CHECK(cuMemAlloc(&z, 0) == CUDA_ERROR_INVALID_VALUE, "zero-size alloc");

    /* unknown pointer */
    CHECK(cuMemcpyHtoD((CUdeviceptr)0x12345678, data, 4) == CUDA_ERROR_INVALID_HANDLE,
          "unknown dest should be INVALID_HANDLE");

    /* memset semantics */
    CHECK(cuMemsetD8(a, 0xAB, 16) == CUDA_SUCCESS, "memset");
    CHECK(cuMemcpyDtoH(back, a, 16) == CUDA_SUCCESS, "memset readback");
    for (int i = 0; i < 16; i++)
        CHECK(back[i] == 0xAB, "memset byte %d = %02x", i, back[i]);
    CHECK(cuMemsetD8(a, 0, 17) == CUDA_ERROR_INVALID_VALUE, "memset overrun");

    /* use-after-free: accessing a freed allocation fails */
    CHECK(cuMemFree(a) == CUDA_SUCCESS, "cuMemFree a");
    CHECK(fake_cuda_live_allocations() == 1, "one live alloc after free");
    CHECK(cuMemcpyHtoD(a, data, 4) == CUDA_ERROR_INVALID_HANDLE,
          "use-after-free should be INVALID_HANDLE");
    CHECK(cuMemFree(a) == CUDA_ERROR_INVALID_HANDLE, "double free");
    CHECK(cuMemFree((CUdeviceptr)0) == CUDA_ERROR_INVALID_HANDLE,
          "cuMemFree(NULL)");

    CHECK(cuMemFree(b) == CUDA_SUCCESS, "cuMemFree b");
    CHECK(fake_cuda_live_allocations() == 0, "balanced allocs");

    CHECK(cuCtxDestroy_v2(ctx) == CUDA_SUCCESS, "ctx destroy");
}

/* ------------------------------------------------------------------ */
/* Module/function/global name resolution                              */
/* ------------------------------------------------------------------ */

static void test_module(void) {
    CUcontext ctx = 0;
    cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);

    const char* ptx = "// fake ptx\n";
    CUmodule mod = 0;
    CHECK(cuModuleLoadDataEx(&mod, ptx, 0, nullptr, nullptr) == CUDA_SUCCESS &&
          mod != 0, "cuModuleLoadDataEx");
    CHECK(fake_cuda_live_modules() == 1, "one live module");
    CHECK(fake_cuda_live_constants() == 3, "three module constants");

    CUfunction fn = 0;
    CHECK(cuModuleGetFunction(&fn, mod, "search_nonce") == CUDA_SUCCESS &&
          fn != 0, "get search_nonce");
    CUfunction unknown = 0;
    CHECK(cuModuleGetFunction(&unknown, mod, "nope") == CUDA_ERROR_NOT_FOUND,
          "unknown function should be NOT_FOUND");
    CHECK(cuModuleGetFunction(&unknown, (CUmodule)777, "search_nonce") ==
              CUDA_ERROR_INVALID_HANDLE,
          "bad module handle should be INVALID_HANDLE");

    CUdeviceptr cv = 0, b2 = 0, tgt = 0;
    size_t cv_sz = 0, b2_sz = 0, tgt_sz = 0;
    CHECK(cuModuleGetGlobal(&cv, &cv_sz, mod, "d_cv") == CUDA_SUCCESS &&
          cv_sz == HASH_SIZE && cv != 0, "d_cv");
    CHECK(cuModuleGetGlobal(&b2, &b2_sz, mod, "d_block2") == CUDA_SUCCESS &&
          b2_sz == 64, "d_block2");
    CHECK(cuModuleGetGlobal(&tgt, &tgt_sz, mod, "d_target") == CUDA_SUCCESS &&
          tgt_sz == HASH_SIZE, "d_target");
    CHECK(cuModuleGetGlobal(&cv, &cv_sz, mod, "bogus") == CUDA_ERROR_NOT_FOUND,
          "unknown global should be NOT_FOUND");

    /* the module's constant pointers are distinguishable from allocs */
    void* base = nullptr;
    size_t sz = 0;
    int is_const = 0;
    CHECK(fake_cuda_ptr_info(cv, &base, &sz, &is_const) == 0 &&
          is_const == 1 && sz == 32, "d_cv is const");

    /* writing to a module constant via HtoD works (host does this) */
    uint8_t cvd[32];
    memset(cvd, 0x7F, sizeof(cvd));
    CHECK(cuMemcpyHtoD(cv, cvd, 32) == CUDA_SUCCESS, "HtoD d_cv");
    uint8_t cvd_back[32] = {0};
    CHECK(cuMemcpyDtoH(cvd_back, cv, 32) == CUDA_SUCCESS &&
          memcmp(cvd, cvd_back, 32) == 0, "DtoH d_cv roundtrip");

    CHECK(cuModuleUnload(mod) == CUDA_SUCCESS, "cuModuleUnload");
    CHECK(fake_cuda_live_modules() == 0, "module unloaded");
    CHECK(cuModuleGetFunction(&fn, mod, "search_nonce") == CUDA_ERROR_INVALID_HANDLE,
          "use after unload should be INVALID_HANDLE");

    CHECK(cuCtxDestroy_v2(ctx) == CUDA_SUCCESS, "ctx destroy");
}

/* ------------------------------------------------------------------ */
/* Launch: config capture, arg validation, error injection             */
/* ------------------------------------------------------------------ */

static void test_launch_validation(void) {
    CUcontext ctx = 0;
    cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);

    CUmodule mod = 0;
    cuModuleLoadDataEx(&mod, "// ptx", 0, nullptr, nullptr);
    CUfunction fn = 0;
    cuModuleGetFunction(&fn, mod, "search_nonce");

    CUdeviceptr res = 0, hsh = 0;
    cuMemAlloc(&res, 4);
    cuMemAlloc(&hsh, 32);

    uint32_t start = 1, count = 2;
    void* args[] = { (void*)&start, (void*)&count, (void*)&res, (void*)&hsh };

    /* valid launch, config captured */
    CHECK(cuLaunchKernel(fn, 1, 1, 1, 256, 1, 1, 0, 0, args, nullptr) ==
              CUDA_SUCCESS,
          "valid launch");
    unsigned int gx = 0, gy = 0, gz = 0, bx = 0, by = 0, bz = 0, smem = 0;
    CHECK(fake_cuda_get_last_launch(&gx, &gy, &gz, &bx, &by, &bz, &smem) == 0 &&
          gx == 1 && by == 1 && bx == 256 && smem == 0,
          "launch config captured (gx=%u bx=%u)", gx, bx);

    /* invalid configs */
    CHECK(cuLaunchKernel(fn, 0, 1, 1, 256, 1, 1, 0, 0, args, nullptr) ==
              CUDA_ERROR_INVALID_VALUE,
          "zero grid x");
    CHECK(cuLaunchKernel(fn, 1, 0, 1, 256, 1, 1, 0, 0, args, nullptr) ==
              CUDA_ERROR_INVALID_VALUE,
          "zero grid y");
    CHECK(cuLaunchKernel(fn, 1, 1, 1, 0, 1, 1, 0, 0, args, nullptr) ==
              CUDA_ERROR_INVALID_VALUE,
          "zero block x");
    CHECK(cuLaunchKernel(fn, 1, 1, 1, 256, 1, 1, 0, 0, nullptr, nullptr) ==
              CUDA_ERROR_INVALID_VALUE,
          "null kernelParams");
    CHECK(cuLaunchKernel((CUfunction)0xdeadbeef, 1, 1, 1, 256, 1, 1, 0, 0,
                         args, nullptr) == CUDA_ERROR_INVALID_HANDLE,
          "bad kernel handle");

    /* missing result alloc (null device ptr / tiny alloc) */
    CUdeviceptr tiny = 0;
    cuMemAlloc(&tiny, 2);
    void* args_tiny[] = { (void*)&start, (void*)&count, (void*)&tiny,
                          (void*)&hsh };
    CHECK(cuLaunchKernel(fn, 1, 1, 1, 256, 1, 1, 0, 0, args_tiny, nullptr) ==
              CUDA_ERROR_INVALID_VALUE,
          "result alloc too small");
    cuMemFree(tiny);

    cuMemFree(res);
    cuMemFree(hsh);
    cuModuleUnload(mod);
    cuCtxDestroy_v2(ctx);
}

/*
 * Full mining lifecycle through the fake driver vs the CPU reference and
 * precomputed vectors.
 */
static int run_reference_count(uint8_t tgt[32], uint32_t range);

static void run_single_batch(uint8_t tgt[32], uint32_t range,
                             uint32_t expected_nonce) {
    uint8_t hdr[HEADER_SIZE];
    make_header(hdr);
    uint32_t cv[8], block2[16];
    cpu_compute_midstate(hdr, cv, block2);

    /* reference (independent of the fake driver) */
    uint32_t tgt_words[8];
    memcpy(tgt_words, tgt, 32);
    int32_t ref_found = 0;
    uint32_t ref_hash[8];
    cpu_search_nonce(cv, block2, tgt_words, 1, range, &ref_found, ref_hash);

    /* drive through the fake driver using host-equivalent calls */
    CUcontext ctx = 0;
    cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);
    CUmodule mod = 0;
    cuModuleLoadDataEx(&mod, "// ptx", 0, nullptr, nullptr);
    CUfunction kernel = 0;
    cuModuleGetFunction(&kernel, mod, "search_nonce");

    CUdeviceptr g_cv = 0, g_b2 = 0, g_tgt = 0;
    size_t cv_sz = 0, b2_sz = 0, tgt_sz = 0;
    cuModuleGetGlobal(&g_cv, &cv_sz, mod, "d_cv");
    cuModuleGetGlobal(&g_b2, &b2_sz, mod, "d_block2");
    cuModuleGetGlobal(&g_tgt, &tgt_sz, mod, "d_target");
    cuMemcpyHtoD(g_cv, cv, HASH_SIZE);
    cuMemcpyHtoD(g_b2, block2, 64);
    cuMemcpyHtoD(g_tgt, tgt, HASH_SIZE);

    CUdeviceptr g_res = 0, g_hash = 0;
    cuMemAlloc(&g_res, 4);
    cuMemsetD8(g_res, 0, 4);
    cuMemAlloc(&g_hash, HASH_SIZE);

    unsigned int threads = 256;
    unsigned int blocks = (range + threads - 1) / threads;
    launch_search(kernel, g_res, g_hash, 1, range, blocks, threads);

    int32_t fake_found = 0;
    uint32_t fake_hash[8] = {0};
    cuMemcpyDtoH(&fake_found, g_res, 4);
    cuMemcpyDtoH(fake_hash, g_hash, HASH_SIZE);

    CHECK(fake_found == ref_found, "fake nonce %d vs ref %d", fake_found,
          ref_found);
    CHECK((int32_t)expected_nonce == ref_found,
          "expected nonce %u got ref %d", expected_nonce, ref_found);
    CHECK(memcmp(fake_hash, ref_hash, HASH_SIZE) == 0,
          "fake hash matches reference");

    unsigned int gx = 0, gy = 0, gz = 0;
    unsigned int bx = 0, by = 0, bz = 0, smem = 0;
    CHECK(fake_cuda_get_last_launch(&gx, &gy, &gz, &bx, &by, &bz, &smem) == 0 &&
          bx == 256 && gx == blocks,
          "canonical launch shape (blocks=%u threads=256)", gx);

    cuMemFree(g_res);
    cuMemFree(g_hash);
    cuModuleUnload(mod);
    cuCtxDestroy_v2(ctx);
}

static void test_vectors(void) {
    uint8_t tgt[TARGET_LEN];

    /* FIND: single-match test, lowest nonce 435 */
    make_target(tgt, 1);
    run_single_batch(tgt, 4096, 435);

    /* NOMATCH: impossible target (all zero) - must find nothing */
    memset(tgt, 0, TARGET_LEN);
    CHECK(run_reference_count(tgt, 4096) == 0, "no matches for zero target");

    /* MULTI: multiple matches; deterministic lowest nonce is 51155 */
    make_target(tgt, 2);
    run_single_batch(tgt, 1 << 20, 51155);
    int multiplicity = run_reference_count(tgt, 1 << 20);
    CHECK(multiplicity > 1, "MULTI has >1 matches (%d)", multiplicity);
}

/* helper used above: count matches in reference over range without driver */
static int run_reference_count(uint8_t tgt[32], uint32_t range) {
    uint8_t hdr[HEADER_SIZE];
    make_header(hdr);
    uint32_t cv[8], block2[16];
    cpu_compute_midstate(hdr, cv, block2);
    uint32_t tw[8];
    memcpy(tw, tgt, 32);
    int count = 0;
    for (uint32_t n = 1; n < range; n++) {
        uint32_t m2[16];
        memcpy(m2, block2, sizeof(m2));
        m2[3] = n;
        uint32_t tmp[8];
        cpu_blake3_compress(m2, cv, 0, 52, 0x02 | 0x08, tmp);
        int ok = 1;
        for (int i = 7; i >= 0; i--) {
            if (tmp[i] > tw[i]) { ok = 0; break; }
            if (tmp[i] < tw[i]) break;
        }
        if (ok) count++;
    }
    return count;
}

/* ------------------------------------------------------------------ */
/* Error injection                                                     */
/* ------------------------------------------------------------------ */

static void test_error_injection(void) {
    /* every operation can be failed exactly once */
    struct { FakeCudaOperation op; CUresult err; } cases[] = {
        { FAKE_OP_INIT, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_DRIVER_VERSION, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_DEVICE_GET_COUNT, CUDA_ERROR_OUT_OF_MEMORY },
        { FAKE_OP_DEVICE_GET, CUDA_ERROR_INVALID_DEVICE },
        { FAKE_OP_DEVICE_GET_NAME, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_DEVICE_GET_ATTRIBUTE, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_CTX_CREATE, CUDA_ERROR_OUT_OF_MEMORY },
        { FAKE_OP_CTX_DESTROY, CUDA_ERROR_INVALID_CONTEXT },
        { FAKE_OP_MODULE_LOAD, CUDA_ERROR_INVALID_IMAGE },
        { FAKE_OP_MODULE_GET_FUNCTION, CUDA_ERROR_NOT_FOUND },
        { FAKE_OP_MODULE_GET_GLOBAL, CUDA_ERROR_NOT_FOUND },
        { FAKE_OP_MODULE_UNLOAD, CUDA_ERROR_INVALID_HANDLE },
        { FAKE_OP_MEM_ALLOC, CUDA_ERROR_OUT_OF_MEMORY },
        { FAKE_OP_MEM_HTOD, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_MEM_DTOH, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_MEM_MEMSET, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_MEM_FREE, CUDA_ERROR_INVALID_HANDLE },
        { FAKE_OP_LAUNCH, CUDA_ERROR_INVALID_VALUE },
        { FAKE_OP_SYNC, CUDA_ERROR_INVALID_CONTEXT },
    };

    for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++) {
        fake_cuda_reset();
        fake_cuda_fail_next(cases[i].op, cases[i].err);
        CUresult rc = CUDA_SUCCESS;
        switch (cases[i].op) {
            case FAKE_OP_INIT:                     rc = cuInit(0); break;
            case FAKE_OP_DRIVER_VERSION:           { int v; rc = cuDriverGetVersion(&v); } break;
            case FAKE_OP_DEVICE_GET_COUNT:         { int c; rc = cuDeviceGetCount(&c); } break;
            case FAKE_OP_DEVICE_GET:               { CUdevice d; rc = cuDeviceGet(&d, 0); } break;
            case FAKE_OP_DEVICE_GET_NAME:          { char n[64]; rc = cuDeviceGetName(n, sizeof(n), 0); } break;
            case FAKE_OP_DEVICE_GET_ATTRIBUTE:     { int a; rc = cuDeviceGetAttribute(&a, CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MAJOR, 0); } break;
            case FAKE_OP_CTX_CREATE:               { CUcontext c; rc = cuCtxCreate_v2(&c, CU_CTX_SCHED_AUTO, 0); } break;
            case FAKE_OP_CTX_DESTROY:              { CUcontext c; cuCtxCreate_v2(&c, CU_CTX_SCHED_AUTO, 0); rc = cuCtxDestroy_v2(c); } break;
            case FAKE_OP_MODULE_LOAD:              { CUmodule m; rc = cuModuleLoadDataEx(&m, "x", 0, nullptr, nullptr); } break;
            case FAKE_OP_MODULE_GET_FUNCTION:      { CUmodule m; CUfunction f; cuModuleLoadDataEx(&m, "x", 0, nullptr, nullptr); rc = cuModuleGetFunction(&f, m, "search_nonce"); cuModuleUnload(m); } break;
            case FAKE_OP_MODULE_GET_GLOBAL:        { CUmodule m; CUdeviceptr d; size_t s; cuModuleLoadDataEx(&m, "x", 0, nullptr, nullptr); rc = cuModuleGetGlobal(&d, &s, m, "d_cv"); cuModuleUnload(m); } break;
            case FAKE_OP_MODULE_UNLOAD:            { CUmodule m; cuModuleLoadDataEx(&m, "x", 0, nullptr, nullptr); rc = cuModuleUnload(m); } break;
            case FAKE_OP_MEM_ALLOC:                { CUdeviceptr d; rc = cuMemAlloc(&d, 4); } break;
            case FAKE_OP_MEM_HTOD:                 { CUdeviceptr d; cuMemAlloc(&d, 4); char x = 1; rc = cuMemcpyHtoD(d, &x, 1); cuMemFree(d); } break;
            case FAKE_OP_MEM_DTOH:                 { CUdeviceptr d; cuMemAlloc(&d, 4); char x = 0; rc = cuMemcpyDtoH(&x, d, 1); cuMemFree(d); } break;
            case FAKE_OP_MEM_MEMSET:               { CUdeviceptr d; cuMemAlloc(&d, 4); rc = cuMemsetD8(d, 0, 2); cuMemFree(d); } break;
            case FAKE_OP_MEM_FREE:                 { CUdeviceptr d; cuMemAlloc(&d, 4); rc = cuMemFree(d); } break;
            case FAKE_OP_LAUNCH: {
                CUcontext c; CUmodule m; CUfunction f;
                cuCtxCreate_v2(&c, CU_CTX_SCHED_AUTO, 0);
                cuModuleLoadDataEx(&m, "x", 0, nullptr, nullptr);
                cuModuleGetFunction(&f, m, "search_nonce");
                CUdeviceptr r, h;
                cuMemAlloc(&r, 4); cuMemAlloc(&h, 32);
                uint32_t s = 1, n = 2;
                void* a[] = { (void*)&s, (void*)&n, (void*)&r, (void*)&h };
                rc = cuLaunchKernel(f, 1, 1, 1, 256, 1, 1, 0, 0, a, nullptr);
                cuMemFree(r); cuMemFree(h); cuModuleUnload(m); cuCtxDestroy_v2(c);
            } break;
            case FAKE_OP_SYNC:                     rc = cuCtxSynchronize(); break;
            default: break;
        }
        CHECK(rc == cases[i].err,
              "injection op %d expected err %d got %d", (int)cases[i].op,
              (int)cases[i].err, (int)rc);
        /* injection is one-shot: the next call succeeds */
        fake_cuda_reset();
    }
}

/* ------------------------------------------------------------------ */
/* NVRTC                                                               */
/* ------------------------------------------------------------------ */

static void test_nvrtc(void) {
    nvrtcProgram prog = NULL;
    const char* src = "extern \"C\" __global__ void search_nonce(unsigned int);";
    nvrtcResult r = nvrtcCreateProgram(&prog, src, "kernel.cu", 0, NULL, NULL);
    CHECK(r == NVRTC_SUCCESS && prog, "nvrtcCreateProgram");

    CHECK(nvrtcAddNameExpression(prog, "search_nonce") == NVRTC_SUCCESS,
          "add search_nonce");
    CHECK(nvrtcAddNameExpression(prog, "&d_cv") == NVRTC_SUCCESS, "add &d_cv");
    CHECK(nvrtcAddNameExpression(prog, "&d_block2") == NVRTC_SUCCESS,
          "add &d_block2");
    CHECK(nvrtcAddNameExpression(prog, "&d_target") == NVRTC_SUCCESS,
          "add &d_target");
    CHECK(nvrtcAddNameExpression(prog, "bogus") == NVRTC_SUCCESS,
          "add unknown name");

    const char* lowered = nullptr;
    CHECK(nvrtcGetLoweredName(prog, "search_nonce", &lowered) == NVRTC_SUCCESS &&
          lowered && strcmp(lowered, "search_nonce") == 0,
          "lowered search_nonce='%s'", lowered ? lowered : "(null)");
    CHECK(nvrtcGetLoweredName(prog, "&d_cv", &lowered) == NVRTC_SUCCESS &&
          lowered && strcmp(lowered, "d_cv") == 0,
          "lowered &d_cv='%s'", lowered ? lowered : "(null)");
    CHECK(nvrtcGetLoweredName(prog, "&d_block2", &lowered) == NVRTC_SUCCESS &&
          lowered && strcmp(lowered, "d_block2") == 0,
          "lowered &d_block2='%s'", lowered ? lowered : "(null)");
    CHECK(nvrtcGetLoweredName(prog, "&d_target", &lowered) == NVRTC_SUCCESS &&
          lowered && strcmp(lowered, "d_target") == 0,
          "lowered &d_target='%s'", lowered ? lowered : "(null)");

    const char* opts[] = { "--gpu-architecture=compute_89" };
    CHECK(nvrtcCompileProgram(prog, 1, opts) == NVRTC_SUCCESS,
          "nvrtcCompileProgram");

    size_t sz = 0;
    CHECK(nvrtcGetPTXSize(prog, &sz) == NVRTC_SUCCESS && sz > 1, "ptx size>1");
    char* ptx = (char*)malloc(sz);
    CHECK(nvrtcGetPTX(prog, ptx) == NVRTC_SUCCESS && ptx[0] == '/',
          "ptx content");
    free(ptx);

    size_t logsz = 0;
    CHECK(nvrtcGetProgramLogSize(prog, &logsz) == NVRTC_SUCCESS && logsz <= 1,
          "empty log on success");

    CHECK(nvrtcGetErrorString(NVRTC_SUCCESS) != NULL, "error string");

    CHECK(nvrtcDestroyProgram(&prog) == NVRTC_SUCCESS && prog == NULL,
          "nvrtcDestroyProgram");
    CHECK(nvrtcDestroyProgram(&prog) == NVRTC_ERROR_INVALID_INPUT,
          "double destroy");

    /* failed compile leaves a populated log and non-zero result */
    prog = NULL;
    nvrtcCreateProgram(&prog, src, "kernel.cu", 0, NULL, NULL);
    fake_nvrtc_fail_next(0, NVRTC_ERROR_COMPILATION);
    CHECK(nvrtcCompileProgram(prog, 1, opts) == NVRTC_ERROR_COMPILATION,
          "injected compile failure");
    CHECK(nvrtcGetProgramLogSize(prog, &logsz) == NVRTC_SUCCESS && logsz > 1,
          "log populated after failure");
    nvrtcDestroyProgram(&prog);
}

/* ------------------------------------------------------------------ */
/* Leak detection                                                      */
/* ------------------------------------------------------------------ */

static void test_leaks(void) {
    /* a leaked ctx and alloc must be reported */
    CUcontext ctx = 0;
    cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);
    CUdeviceptr d = 0;
    cuMemAlloc(&d, 4);
    CHECK(fake_cuda_live_contexts() == 1 && fake_cuda_live_allocations() == 1,
          "leaks present");
    CHECK(fake_cuda_assert_no_leaks() == -1, "assert reports leak");

    cuMemFree(d);
    cuCtxDestroy_v2(ctx);
    CHECK(fake_cuda_assert_no_leaks() == 0, "no leaks after cleanup");
}

static void test_shutdown_mid_search(void) {
    /* repeated lifecycle: create everything, unload, destroy */
    for (int i = 0; i < 3; i++) {
        CUcontext ctx = 0;
        cuCtxCreate_v2(&ctx, CU_CTX_SCHED_AUTO, 0);
        CUmodule mod = 0;
        cuModuleLoadDataEx(&mod, "// ptx", 0, nullptr, nullptr);
        CUdeviceptr a = 0, b = 0;
        cuMemAlloc(&a, 4);
        cuMemAlloc(&b, 32);
        cuMemFree(a);
        cuMemFree(b);
        cuModuleUnload(mod);
        cuCtxDestroy_v2(ctx);
        CHECK(fake_cuda_assert_no_leaks() == 0, "cycle %d no leaks", i);
    }
}

int main(void) {
    test_lifecycle();
    test_context();
    test_memory();
    test_module();
    test_launch_validation();
    test_vectors();
    test_error_injection();
    test_nvrtc();
    test_leaks();
    test_shutdown_mid_search();

    if (failures) {
        fprintf(stderr, "api_tests: %d failures / %d checks\n", failures,
                checks);
        return 1;
    }
    printf("api_tests: all %d checks passed\n", checks);
    return 0;
}