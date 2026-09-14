#include <iostream>
#include <fstream>
#include <sstream>
#include <cstdio>
#include <cstring>
#include <cstdlib>
#include <thread>
#include <chrono>
#include <atomic>
#include <vector>
#include <dlfcn.h>
#include "cu.h"

#define HEADER_SIZE 180
#define HASH_SIZE 32

CUcontext g_ctx = 0;
CUmodule g_module = 0;
CUfunction g_kernel = 0;
CUdeviceptr g_cv  = 0;
CUdeviceptr g_b2  = 0;
CUdeviceptr g_tgt = 0;
CUdeviceptr g_result = 0;
CUdeviceptr g_hash = 0;
nvrtcProgram g_prog = 0;
size_t g_cv_size = 0, g_b2_size = 0, g_tgt_size = 0;

std::atomic<bool> g_stop{false};
std::atomic<bool> g_searching{false};

pfn_cuInit               cuInit               = nullptr;
pfn_cuDriverGetVersion   cuDriverGetVersion   = nullptr;
pfn_cuDeviceGetCount     cuDeviceGetCount     = nullptr;
pfn_cuDeviceGet          cuDeviceGet          = nullptr;
pfn_cuDeviceGetName      cuDeviceGetName      = nullptr;
pfn_cuDeviceGetAttribute cuDeviceGetAttribute = nullptr;
pfn_cuCtxCreate_v2       cuCtxCreate_v2       = nullptr;
pfn_cuCtxDestroy_v2      cuCtxDestroy_v2      = nullptr;
pfn_cuModuleLoadDataEx   cuModuleLoadDataEx   = nullptr;
pfn_cuModuleGetFunction  cuModuleGetFunction  = nullptr;
pfn_cuModuleGetGlobal    cuModuleGetGlobal    = nullptr;
pfn_cuModuleUnload       cuModuleUnload       = nullptr;
pfn_cuMemAlloc           cuMemAlloc           = nullptr;
pfn_cuMemcpyHtoD         cuMemcpyHtoD         = nullptr;
pfn_cuMemcpyDtoH         cuMemcpyDtoH         = nullptr;
pfn_cuMemsetD8           cuMemsetD8           = nullptr;
pfn_cuLaunchKernel       cuLaunchKernel       = nullptr;
pfn_cuCtxSynchronize     cuCtxSynchronize     = nullptr;
pfn_cuGetErrorString     cuGetErrorString     = nullptr;
pfn_cuGetErrorName       cuGetErrorName       = nullptr;

pfn_nvrtcCreateProgram       nvrtcCreateProgram       = nullptr;
pfn_nvrtcCompileProgram      nvrtcCompileProgram      = nullptr;
pfn_nvrtcGetPTXSize          nvrtcGetPTXSize          = nullptr;
pfn_nvrtcGetPTX              nvrtcGetPTX              = nullptr;
pfn_nvrtcGetProgramLogSize   nvrtcGetProgramLogSize   = nullptr;
pfn_nvrtcGetProgramLog       nvrtcGetProgramLog       = nullptr;
pfn_nvrtcDestroyProgram      nvrtcDestroyProgram      = nullptr;
pfn_nvrtcGetErrorString      nvrtcGetErrorString      = nullptr;

static std::string readFile(const std::string& path) {
    std::ifstream f(path, std::ios::binary);
    if (!f) return "";
    std::stringstream ss;
    ss << f.rdbuf();
    return ss.str();
}

static std::string cudaErrorString(CUresult err) {
    if (cuGetErrorString && cuGetErrorName) {
        const char* name = nullptr;
        const char* str = nullptr;
        if (cuGetErrorName(err, &name) == CUDA_SUCCESS &&
            cuGetErrorString(err, &str) == CUDA_SUCCESS && str) {
            std::string s = name ? name : "";
            s += ": ";
            s += str;
            return s;
        }
    }
    return "CUDA_ERROR(" + std::to_string(err) + ")";
}

static std::string nvrtcErrorString(nvrtcResult err) {
    if (nvrtcGetErrorString) {
        const char* s = nvrtcGetErrorString(err);
        if (s) return s;
    }
    return "NVRTC_ERROR(" + std::to_string(err) + ")";
}

static int cu_load(void) {
#if defined(__linux__)
    void* h = dlopen("libcuda.so.1", RTLD_NOW);
    if (!h) h = dlopen("libcuda.so", RTLD_NOW);
    if (!h) return -1;
    cuInit               = (pfn_cuInit)              dlsym(h, "cuInit");
    cuDriverGetVersion   = (pfn_cuDriverGetVersion)  dlsym(h, "cuDriverGetVersion");
    cuDeviceGetCount     = (pfn_cuDeviceGetCount)    dlsym(h, "cuDeviceGetCount");
    cuDeviceGet          = (pfn_cuDeviceGet)         dlsym(h, "cuDeviceGet");
    cuDeviceGetName      = (pfn_cuDeviceGetName)     dlsym(h, "cuDeviceGetName");
    cuDeviceGetAttribute = (pfn_cuDeviceGetAttribute)dlsym(h, "cuDeviceGetAttribute");
    cuCtxCreate_v2       = (pfn_cuCtxCreate_v2)      dlsym(h, "cuCtxCreate_v2");
    cuCtxDestroy_v2      = (pfn_cuCtxDestroy_v2)     dlsym(h, "cuCtxDestroy_v2");
    cuModuleLoadDataEx   = (pfn_cuModuleLoadDataEx)  dlsym(h, "cuModuleLoadDataEx");
    cuModuleGetFunction  = (pfn_cuModuleGetFunction) dlsym(h, "cuModuleGetFunction");
    cuModuleGetGlobal    = (pfn_cuModuleGetGlobal)   dlsym(h, "cuModuleGetGlobal");
    cuModuleUnload       = (pfn_cuModuleUnload)      dlsym(h, "cuModuleUnload");
    cuMemAlloc           = (pfn_cuMemAlloc)          dlsym(h, "cuMemAlloc");
    cuMemcpyHtoD         = (pfn_cuMemcpyHtoD)        dlsym(h, "cuMemcpyHtoD");
    cuMemcpyDtoH         = (pfn_cuMemcpyDtoH)        dlsym(h, "cuMemcpyDtoH");
    cuMemsetD8           = (pfn_cuMemsetD8)          dlsym(h, "cuMemsetD8");
    cuLaunchKernel       = (pfn_cuLaunchKernel)      dlsym(h, "cuLaunchKernel");
    cuCtxSynchronize     = (pfn_cuCtxSynchronize)    dlsym(h, "cuCtxSynchronize");
    cuGetErrorString     = (pfn_cuGetErrorString)    dlsym(h, "cuGetErrorString");
    cuGetErrorName       = (pfn_cuGetErrorName)      dlsym(h, "cuGetErrorName");
    return 0;
#else
    return -1;
#endif
}

static int nvrtc_load(void) {
#if defined(__linux__)
    void* h = dlopen("libnvrtc.so.12", RTLD_NOW);
    if (!h) h = dlopen("libnvrtc.so", RTLD_NOW);
    if (!h) return -1;
    nvrtcCreateProgram     = (pfn_nvrtcCreateProgram)    dlsym(h, "nvrtcCreateProgram");
    nvrtcCompileProgram    = (pfn_nvrtcCompileProgram)   dlsym(h, "nvrtcCompileProgram");
    nvrtcGetPTXSize        = (pfn_nvrtcGetPTXSize)       dlsym(h, "nvrtcGetPTXSize");
    nvrtcGetPTX            = (pfn_nvrtcGetPTX)           dlsym(h, "nvrtcGetPTX");
    nvrtcGetProgramLogSize = (pfn_nvrtcGetProgramLogSize)dlsym(h, "nvrtcGetProgramLogSize");
    nvrtcGetProgramLog     = (pfn_nvrtcGetProgramLog)    dlsym(h, "nvrtcGetProgramLog");
    nvrtcDestroyProgram    = (pfn_nvrtcDestroyProgram)   dlsym(h, "nvrtcDestroyProgram");
    nvrtcGetErrorString    = (pfn_nvrtcGetErrorString)   dlsym(h, "nvrtcGetErrorString");
    return 0;
#else
    return -1;
#endif
}

static std::string extractLog(nvrtcProgram prog) {
    size_t len = 0;
    if (nvrtcGetProgramLogSize(prog, &len) != NVRTC_SUCCESS) return "";
    if (len <= 1) return "";
    std::string log(len, 0);
    nvrtcGetProgramLog(prog, &log[0]);
    return log;
}

static bool compileKernel(const std::string& src, const std::vector<std::string>& archs) {
    for (size_t i = 0; i < archs.size(); i++) {
        std::string opt = "--gpu-architecture=" + archs[i];
        const char* options[] = { opt.c_str() };
        if (g_prog) {
            nvrtcDestroyProgram(&g_prog);
            g_prog = 0;
        }
        nvrtcResult rc = nvrtcCreateProgram(&g_prog, src.c_str(), "kernel.cu", 0, nullptr, nullptr);
        if (rc != NVRTC_SUCCESS) {
            std::cerr << "nvrtcCreateProgram failed: " << nvrtcErrorString(rc) << "\n";
            return false;
        }
        rc = nvrtcCompileProgram(g_prog, 1, options);
        if (rc == NVRTC_SUCCESS) return true;
        std::string log = extractLog(g_prog);
        if (i + 1 < archs.size()) {
            std::cerr << "nvrtc failed for " << archs[i] << ", retrying lower arch:\n"
                      << log << "\n";
        } else {
            std::cerr << "nvrtcCompileProgram failed: " << nvrtcErrorString(rc) << "\n"
                      << log << "\n";
        }
    }
    return false;
}

static bool loadModuleFromPTX(void) {
    size_t ptx_size = 0;
    if (nvrtcGetPTXSize(g_prog, &ptx_size) != NVRTC_SUCCESS || ptx_size == 0) {
        std::cerr << "nvrtcGetPTXSize failed\n";
        return false;
    }
    std::string ptx(ptx_size, 0);
    if (nvrtcGetPTX(g_prog, &ptx[0]) != NVRTC_SUCCESS) {
        std::cerr << "nvrtcGetPTX failed\n";
        return false;
    }

    CUresult rc = cuModuleLoadDataEx(&g_module, ptx.c_str(), 0, nullptr, nullptr);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuModuleLoadDataEx failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuModuleGetFunction(&g_kernel, g_module, "search_nonce");
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuModuleGetFunction failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuModuleGetGlobal(&g_cv, &g_cv_size, g_module, "d_cv");
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuModuleGetGlobal d_cv failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuModuleGetGlobal(&g_b2, &g_b2_size, g_module, "d_block2");
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuModuleGetGlobal d_block2 failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuModuleGetGlobal(&g_tgt, &g_tgt_size, g_module, "d_target");
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuModuleGetGlobal d_target failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuMemAlloc(&g_result, 4);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuMemAlloc result failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuMemAlloc(&g_hash, HASH_SIZE);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuMemAlloc hash failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    return true;
}

static bool initCUDA(const std::string& kernelDir, int deviceIdx) {
    if (cu_load() != 0) {
        std::cerr << "CUDA library (libcuda.so) not found\n";
        return false;
    }
    if (nvrtc_load() != 0) {
        std::cerr << "nvrtc library (libnvrtc.so) not found; is the CUDA toolkit installed?\n";
        return false;
    }

    CUresult rc = cuInit(0);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuInit failed: " << cudaErrorString(rc) << "\n";
        return false;
    }

    int ndevices = 0;
    rc = cuDeviceGetCount(&ndevices);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuDeviceGetCount failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    if (ndevices == 0) {
        std::cerr << "no CUDA devices found\n";
        return false;
    }

    std::cerr << "CUDA devices found: " << ndevices << "\n";
    for (int i = 0; i < ndevices; i++) {
        char name[256] = {0};
        CUdevice dev;
        cuDeviceGet(&dev, i);
        cuDeviceGetName(name, sizeof(name), dev);
        std::cerr << "  device[" << i << "]: " << name << "\n" << std::flush;
    }

    int chosen = -1;
    if (deviceIdx < 0) {
        chosen = 0;
    } else if (deviceIdx < ndevices) {
        chosen = deviceIdx;
    } else {
        std::cerr << "device index " << deviceIdx << " out of range (0-"
                  << (ndevices - 1) << ")\n";
        return false;
    }

    CUdevice dev;
    cuDeviceGet(&dev, chosen);
    char devname[256] = {0};
    cuDeviceGetName(devname, sizeof(devname), dev);
    std::cerr << "GPU: " << devname << "\n" << std::flush;

    int driverVersion = 0;
    if (cuDriverGetVersion) cuDriverGetVersion(&driverVersion);
    std::cerr << "CUDA driver version: " << (driverVersion / 1000) << "."
              << ((driverVersion % 1000) / 10) << "\n" << std::flush;

    rc = cuCtxCreate_v2(&g_ctx, CU_CTX_SCHED_AUTO, dev);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuCtxCreate_v2 failed: " << cudaErrorString(rc) << "\n";
        return false;
    }

    int major = 0, minor = 0;
    cuDeviceGetAttribute(&major, CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MAJOR, dev);
    cuDeviceGetAttribute(&minor, CU_DEVICE_ATTRIBUTE_COMPUTE_CAPABILITY_MINOR, dev);
    char cc[16];
    snprintf(cc, sizeof(cc), "compute_%d%d", major, minor);
    std::cerr << "compute capability: " << major << "." << minor << "\n" << std::flush;

    std::string kernel_src = readFile(kernelDir + "/kernel.cu");
    if (kernel_src.empty()) {
        std::cerr << "kernel source not found: " << kernelDir << "/kernel.cu\n";
        return false;
    }

    std::vector<std::string> archs;
    archs.push_back(cc);
    archs.push_back("compute_60");
    archs.push_back("compute_50");

    if (!compileKernel(kernel_src, archs)) return false;
    if (!loadModuleFromPTX()) return false;

    return true;
}

static std::string bytesToHex(const uint8_t* data, size_t len) {
    static const char hex[] = "0123456789abcdef";
    std::string out(len * 2, 0);
    for (size_t i = 0; i < len; i++) {
        out[i*2 + 0] = hex[data[i] >> 4];
        out[i*2 + 1] = hex[data[i] & 0xF];
    }
    return out;
}

static bool hexToBytes(const std::string& hex, std::vector<uint8_t>& out) {
    std::string h = hex;
    if (h.size() >= 2 && h[0] == '0' && h[1] == 'x') h = h.substr(2);
    if (h.size() % 2 != 0) return false;
    out.resize(h.size() / 2);
    for (size_t i = 0; i < out.size(); i++) {
        uint8_t hi = 0, lo = 0;
        char c = h[i*2];
        hi = (c >= '0' && c <= '9') ? c - '0' : (c >= 'a' && c <= 'f') ? c - 'a' + 10 : (c >= 'A' && c <= 'F') ? c - 'A' + 10 : 0;
        c = h[i*2+1];
        lo = (c >= '0' && c <= '9') ? c - '0' : (c >= 'a' && c <= 'f') ? c - 'a' + 10 : (c >= 'A' && c <= 'F') ? c - 'A' + 10 : 0;
        out[i] = (hi << 4) | lo;
    }
    return true;
}

static void setNonce(uint8_t* header, uint32_t nonce) {
    header[140] = (uint8_t)((nonce >>  0) & 0xFF);
    header[141] = (uint8_t)((nonce >>  8) & 0xFF);
    header[142] = (uint8_t)((nonce >> 16) & 0xFF);
    header[143] = (uint8_t)((nonce >> 24) & 0xFF);
}

static const uint32_t B3_IV[8] = {
    0x6A09E667U, 0xBB67AE85U, 0x3C6EF372U, 0xA54FF53AU,
    0x510E527FU, 0x9B05688CU, 0x1F83D9ABU, 0x5BE0CD19U
};

static inline uint32_t b3_rotr32(uint32_t x, int n) {
    return (x >> n) | (x << (32 - n));
}

static inline void b3_g(uint32_t* v, int a, int b, int c, int d,
                        uint32_t x, uint32_t y) {
    v[a] = v[a] + v[b] + x;
    v[d] = b3_rotr32(v[d] ^ v[a], 16);
    v[c] = v[c] + v[d];
    v[b] = b3_rotr32(v[b] ^ v[c], 12);
    v[a] = v[a] + v[b] + y;
    v[d] = b3_rotr32(v[d] ^ v[a], 8);
    v[c] = v[c] + v[d];
    v[b] = b3_rotr32(v[b] ^ v[c], 7);
}

static void b3_compress(const uint32_t* m, const uint32_t* cv,
                        uint32_t counter, uint32_t block_len,
                        uint32_t flags, uint32_t* out) {
    uint32_t v[16];
    v[ 0] = cv[0]; v[ 1] = cv[1]; v[ 2] = cv[2]; v[ 3] = cv[3];
    v[ 4] = cv[4]; v[ 5] = cv[5]; v[ 6] = cv[6]; v[ 7] = cv[7];
    v[ 8] = B3_IV[0]; v[ 9] = B3_IV[1]; v[10] = B3_IV[2]; v[11] = B3_IV[3];
    v[12] = counter; v[13] = 0; v[14] = block_len; v[15] = flags;

    uint32_t w[16], t[16];
    memcpy(w, m, 64);
    for (int r = 0; r < 7; r++) {
        b3_g(v,0,4,8,12, w[0],w[1]); b3_g(v,1,5,9,13, w[2],w[3]);
        b3_g(v,2,6,10,14, w[4],w[5]); b3_g(v,3,7,11,15, w[6],w[7]);
        b3_g(v,0,5,10,15, w[8],w[9]); b3_g(v,1,6,11,12, w[10],w[11]);
        b3_g(v,2,7,8,13, w[12],w[13]); b3_g(v,3,4,9,14, w[14],w[15]);
        if (r == 6) break;
        memcpy(t, w, 64);
        w[0]=t[2];w[1]=t[6];w[2]=t[3];w[3]=t[10];w[4]=t[7];w[5]=t[0];
        w[6]=t[4];w[7]=t[13];w[8]=t[1];w[9]=t[11];w[10]=t[12];w[11]=t[5];
        w[12]=t[9];w[13]=t[14];w[14]=t[15];w[15]=t[8];
    }

    for (int i = 0; i < 8; i++) {
        v[i]     ^= v[i + 8];
        v[i + 8] ^= cv[i];
    }
    for (int i = 0; i < 8; i++) out[i] = v[i];
}

static void computeMidstate(const uint8_t* header, uint32_t cv[8],
                            uint32_t block2[16]) {
    uint32_t m[16], tmp[8];
    for (int i = 0; i < 8; i++) cv[i] = B3_IV[i];

    memcpy(m, header, 64);
    b3_compress(m, cv, 0, 64, 0x01, tmp);
    memcpy(cv, tmp, 32);

    memcpy(m, header + 64, 64);
    b3_compress(m, cv, 0, 64, 0x00, tmp);
    memcpy(cv, tmp, 32);

    memset(block2, 0, 64);
    memcpy(block2, header + 128, 52);
    block2[3] = 0;
}

static bool search_gpu(const uint32_t* cv, const uint32_t* block2,
                       const std::vector<uint8_t>& target,
                       uint32_t start_nonce, uint32_t batch_size,
                       uint32_t* found_nonce, std::vector<uint8_t>* found_hash) {
    if (cuMemcpyHtoD(g_cv, cv, 32) != CUDA_SUCCESS) return false;
    if (cuMemcpyHtoD(g_b2, block2, 64) != CUDA_SUCCESS) return false;
    if (cuMemcpyHtoD(g_tgt, target.data(), HASH_SIZE) != CUDA_SUCCESS) return false;
    if (cuMemsetD8(g_result, 0, 4) != CUDA_SUCCESS) return false;

    unsigned int threads = 256;
    unsigned int blocks = (batch_size + threads - 1) / threads;

    void* args[] = {
        (void*)&start_nonce,
        (void*)&batch_size,
        (void*)&g_result,
        (void*)&g_hash,
    };

    CUresult rc = cuLaunchKernel(g_kernel, blocks, 1, 1,
                                 threads, 1, 1, 0, 0, args, nullptr);
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuLaunchKernel failed: " << cudaErrorString(rc) << "\n";
        return false;
    }
    rc = cuCtxSynchronize();
    if (rc != CUDA_SUCCESS) {
        std::cerr << "cuCtxSynchronize failed: " << cudaErrorString(rc) << "\n";
        return false;
    }

    if (cuMemcpyDtoH(found_nonce, g_result, 4) != CUDA_SUCCESS) return false;

    if (*found_nonce != 0) {
        found_hash->resize(HASH_SIZE);
        if (cuMemcpyDtoH(found_hash->data(), g_hash, HASH_SIZE) != CUDA_SUCCESS) return false;
    }

    return true;
}

void handleWork(const std::vector<uint8_t>& header, const std::vector<uint8_t>& target) {
    uint32_t cv[8];
    uint32_t block2[16];
    computeMidstate(header.data(), cv, block2);

    uint32_t start_nonce = 1;
    uint32_t batch_size = 64 * 1024 * 1024;
    uint32_t found_nonce = 0;
    std::vector<uint8_t> found_hash;
    uint64_t nonces_checked = 0;

    g_searching = true;

    while (!g_stop.load() && found_nonce == 0) {
        uint32_t nonce_out = 0;
        std::vector<uint8_t> hash_out;

        if (!search_gpu(cv, block2, target, start_nonce, batch_size, &nonce_out, &hash_out)) {
            std::cout << "{\"type\":\"error\",\"msg\":\"GPU search failed\"}\n" << std::flush;
            g_searching = false;
            return;
        }

        nonces_checked += batch_size;

        if (nonce_out != 0) {
            found_nonce = nonce_out;
            found_hash = hash_out;
            break;
        }

        uint32_t next_nonce = start_nonce + batch_size;
        if (next_nonce < start_nonce) {
            start_nonce = 1;
            break;
        }
        start_nonce = next_nonce;

        if ((nonces_checked / batch_size) % 8 == 0) {
            std::cout << "{\"type\":\"progress\",\"nonces_checked\":" << nonces_checked << "}\n" << std::flush;
        }
        if ((nonces_checked / batch_size) % 256 == 0) {
            std::cerr << "GPU checked " << nonces_checked << " nonces, still searching...\n" << std::flush;
        }
    }

    g_searching = false;

    if (found_nonce != 0) {
        std::vector<uint8_t> sol_header = header;
        setNonce(sol_header.data(), found_nonce);

        std::string header_hex = bytesToHex(sol_header.data(), HEADER_SIZE);
        std::cout << "{\"type\":\"solution\",\"nonce\":" << found_nonce
                  << ",\"nonces_checked\":" << nonces_checked
                  << ",\"header\":\"" << header_hex << "\"}\n" << std::flush;
    } else if (!g_stop.load()) {
        std::cout << "{\"type\":\"searched\",\"nonces_checked\":" << nonces_checked << "}\n" << std::flush;
    }
}

static bool parseMessage(const std::string& line) {
    std::string trimmed = line;
    while (!trimmed.empty() && (trimmed.back() == '\n' || trimmed.back() == '\r' || trimmed.back() == ' ' || trimmed.back() == '\t')) {
        trimmed.pop_back();
    }
    if (trimmed.empty()) return true;

    if (trimmed.find("\"type\"") == std::string::npos) return true;

    auto extractStr = [&](const std::string& key) -> std::string {
        size_t kp = trimmed.find("\"" + key + "\"");
        if (kp == std::string::npos) return "";
        size_t vp = trimmed.find(':', kp);
        if (vp == std::string::npos) return "";
        size_t sp = trimmed.find('\"', vp + 1);
        if (sp == std::string::npos) return "";
        size_t ep = trimmed.find('\"', sp + 1);
        if (ep == std::string::npos) return "";
        return trimmed.substr(sp + 1, ep - sp - 1);
    };

    auto extractInt = [&](const std::string& key, uint64_t def = 0) -> uint64_t {
        size_t kp = trimmed.find("\"" + key + "\"");
        if (kp == std::string::npos) return def;
        size_t vp = trimmed.find(':', kp);
        if (vp == std::string::npos) return def;
        size_t end = vp + 1;
        while (end < trimmed.size() && (trimmed[end] == ' ' || trimmed[end] == '\t')) end++;
        return std::strtoull(trimmed.c_str() + end, nullptr, 10);
    };

    std::string type = extractStr("type");

    if (type == "stop") {
        g_stop = true;
        std::cout << "{\"type\":\"stopped\"}\n" << std::flush;
        return true;
    }
    if (type == "ping") {
        std::cout << "{\"type\":\"pong\"}\n" << std::flush;
        return true;
    }
    if (type == "work") {
        g_stop = false;
        std::vector<uint8_t> header_bytes, target_bytes;
        std::string header_hex = extractStr("header");
        std::string target_hex = extractStr("target");

        if (header_hex.empty() || target_hex.empty()) {
            std::cout << "{\"type\":\"error\",\"msg\":\"missing header or target\"}\n" << std::flush;
            return true;
        }

        if (!hexToBytes(header_hex, header_bytes) || header_bytes.size() < HEADER_SIZE) {
            std::cout << "{\"type\":\"error\",\"msg\":\"invalid header hex\"}\n" << std::flush;
            return true;
        }
        if (header_bytes.size() > HEADER_SIZE) header_bytes.resize(HEADER_SIZE);

        if (!hexToBytes(target_hex, target_bytes) || target_bytes.size() != HASH_SIZE) {
            std::cout << "{\"type\":\"error\",\"msg\":\"invalid target hex\"}\n" << std::flush;
            return true;
        }

        handleWork(header_bytes, target_bytes);
        return true;
    }

    return true;
}

int main(int argc, char* argv[]) {
#if defined(__linux__)
    dlopen(nullptr, RTLD_NOW);
#endif

    std::string kernelDir = ".";
    if (argc > 1) kernelDir = argv[1];

    int deviceIdx = -1;
    if (argc > 2) deviceIdx = atoi(argv[2]);

    if (!initCUDA(kernelDir, deviceIdx)) {
        std::cerr << "CUDA init failed\n";
        return 1;
    }

    std::string line;
    while (std::getline(std::cin, line)) {
        if (!parseMessage(line)) break;
    }

    if (g_prog) nvrtcDestroyProgram(&g_prog);
    if (g_module && cuModuleUnload) cuModuleUnload(g_module);
    if (g_ctx) cuCtxDestroy_v2(g_ctx);

    return 0;
}