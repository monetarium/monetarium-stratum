#include <iostream>
#include <fstream>
#include <sstream>
#include <cstring>
#include <cstdlib>
#include <thread>
#include <chrono>
#include <atomic>
#include <vector>
#include <dlfcn.h>
#include <unistd.h>
#include <fcntl.h>
#include "cl.h"

#define HEADER_SIZE 180
#define HASH_SIZE 32

cl_context g_ctx = 0;
cl_command_queue g_queue = 0;
cl_program g_program = 0;
cl_kernel g_kernel = 0;
std::atomic<bool> g_stop{false};
std::atomic<bool> g_searching{false};

pfn_clGetPlatformIDs        clGetPlatformIDs        = nullptr;
pfn_clGetPlatformInfo       clGetPlatformInfo       = nullptr;
pfn_clGetDeviceIDs          clGetDeviceIDs          = nullptr;
pfn_clGetDeviceInfo          clGetDeviceInfo         = nullptr;
pfn_clCreateContext         clCreateContext         = nullptr;
pfn_clCreateCommandQueue    clCreateCommandQueue    = nullptr;
pfn_clCreateBuffer          clCreateBuffer          = nullptr;
pfn_clCreateProgramWithSource clCreateProgramWithSource = nullptr;
pfn_clBuildProgram          clBuildProgram          = nullptr;
pfn_clCreateKernel          clCreateKernel          = nullptr;
pfn_clSetKernelArg          clSetKernelArg          = nullptr;
pfn_clEnqueueNDRangeKernel  clEnqueueNDRangeKernel  = nullptr;
pfn_clWaitForEvents         clWaitForEvents         = nullptr;
pfn_clReleaseEvent          clReleaseEvent          = nullptr;
pfn_clEnqueueReadBuffer     clEnqueueReadBuffer     = nullptr;
pfn_clEnqueueFillBuffer     clEnqueueFillBuffer     = nullptr;
pfn_clReleaseMemObject      clReleaseMemObject      = nullptr;
pfn_clReleaseKernel         clReleaseKernel         = nullptr;
pfn_clReleaseProgram        clReleaseProgram        = nullptr;
pfn_clReleaseCommandQueue   clReleaseCommandQueue   = nullptr;
pfn_clReleaseContext        clReleaseContext        = nullptr;
pfn_clGetProgramBuildInfo   clGetProgramBuildInfo   = nullptr;

std::string readFile(const std::string& path) {
    std::ifstream f(path, std::ios::binary);
    if (!f) return "";
    std::stringstream ss;
    ss << f.rdbuf();
    return ss.str();
}

std::string getCLError(cl_int err) {
    switch (err) {
        case CL_SUCCESS: return "CL_SUCCESS";
        case CL_DEVICE_NOT_FOUND: return "CL_DEVICE_NOT_FOUND";
        case CL_DEVICE_NOT_AVAILABLE: return "CL_DEVICE_NOT_AVAILABLE";
        case CL_COMPILER_NOT_AVAILABLE: return "CL_COMPILER_NOT_AVAILABLE";
        case CL_MEM_OBJECT_ALLOCATION_FAILURE: return "CL_MEM_OBJECT_ALLOCATION_FAILURE";
        case CL_OUT_OF_RESOURCES: return "CL_OUT_OF_RESOURCES";
        case CL_OUT_OF_HOST_MEMORY: return "CL_OUT_OF_HOST_MEMORY";
        case CL_INVALID_PROGRAM: return "CL_INVALID_PROGRAM";
        case CL_INVALID_KERNEL: return "CL_INVALID_KERNEL";
        case CL_INVALID_ARG_INDEX: return "CL_INVALID_ARG_INDEX";
        case CL_INVALID_ARG_VALUE: return "CL_INVALID_ARG_VALUE";
        case CL_INVALID_MEM_OBJECT: return "CL_INVALID_MEM_OBJECT";
        case CL_INVALID_SAMPLER: return "CL_INVALID_SAMPLER";
        case CL_INVALID_BINARY: return "CL_INVALID_BINARY";
        case CL_INVALID_BUILD_OPTIONS: return "CL_INVALID_BUILD_OPTIONS";
        case CL_BUILD_PROGRAM_FAILURE: return "CL_BUILD_PROGRAM_FAILURE";
        case CL_INVALID_COMMAND_QUEUE: return "CL_INVALID_COMMAND_QUEUE";
        case CL_INVALID_WORK_GROUP_SIZE: return "CL_INVALID_WORK_GROUP_SIZE";
        default: return "CL_UNKNOWN(" + std::to_string(err) + ")";
    }
}

int cl_load(void) {
#if defined(__linux__)
    void* h = dlopen("libOpenCL.so.1", RTLD_NOW);
    if (!h) h = dlopen("libOpenCL.so", RTLD_NOW);
    if (!h) return -1;
    clGetPlatformIDs        = (pfn_clGetPlatformIDs)       dlsym(h, "clGetPlatformIDs");
    clGetPlatformInfo       = (pfn_clGetPlatformInfo)      dlsym(h, "clGetPlatformInfo");
    clGetDeviceIDs          = (pfn_clGetDeviceIDs)         dlsym(h, "clGetDeviceIDs");
    clGetDeviceInfo          = (pfn_clGetDeviceInfo)        dlsym(h, "clGetDeviceInfo");
    clCreateContext          = (pfn_clCreateContext)        dlsym(h, "clCreateContext");
    clCreateCommandQueue     = (pfn_clCreateCommandQueue)   dlsym(h, "clCreateCommandQueue");
    clCreateBuffer           = (pfn_clCreateBuffer)        dlsym(h, "clCreateBuffer");
    clCreateProgramWithSource = (pfn_clCreateProgramWithSource) dlsym(h, "clCreateProgramWithSource");
    clBuildProgram           = (pfn_clBuildProgram)         dlsym(h, "clBuildProgram");
    clCreateKernel           = (pfn_clCreateKernel)         dlsym(h, "clCreateKernel");
    clSetKernelArg           = (pfn_clSetKernelArg)        dlsym(h, "clSetKernelArg");
    clEnqueueNDRangeKernel   = (pfn_clEnqueueNDRangeKernel) dlsym(h, "clEnqueueNDRangeKernel");
    clWaitForEvents          = (pfn_clWaitForEvents)        dlsym(h, "clWaitForEvents");
    clReleaseEvent           = (pfn_clReleaseEvent)        dlsym(h, "clReleaseEvent");
    clEnqueueReadBuffer      = (pfn_clEnqueueReadBuffer)    dlsym(h, "clEnqueueReadBuffer");
    clEnqueueFillBuffer      = (pfn_clEnqueueFillBuffer)    dlsym(h, "clEnqueueFillBuffer");
    clReleaseMemObject       = (pfn_clReleaseMemObject)    dlsym(h, "clReleaseMemObject");
    clReleaseKernel          = (pfn_clReleaseKernel)       dlsym(h, "clReleaseKernel");
    clReleaseProgram         = (pfn_clReleaseProgram)      dlsym(h, "clReleaseProgram");
    clReleaseCommandQueue    = (pfn_clReleaseCommandQueue)  dlsym(h, "clReleaseCommandQueue");
    clReleaseContext         = (pfn_clReleaseContext)       dlsym(h, "clReleaseContext");
    clGetProgramBuildInfo    = (pfn_clGetProgramBuildInfo)  dlsym(h, "clGetProgramBuildInfo");
    return 0;
#else
    return -1;
#endif
}

bool initOpenCL(const std::string& kernelDir) {
    if (cl_load() != 0) {
        std::cerr << "OpenCL library not found\n";
        return false;
    }

    cl_uint nplatforms = 0;
    cl_int err = clGetPlatformIDs(0, nullptr, &nplatforms);
    if (err != CL_SUCCESS || nplatforms == 0) {
        std::cerr << "no OpenCL platforms found\n";
        return false;
    }

    std::vector<cl_platform_id> platforms(nplatforms);
    clGetPlatformIDs(nplatforms, platforms.data(), nullptr);

    cl_platform_id chosenPlatform = 0;
    cl_device_id chosenDevice = 0;
    char devname[256];

    for (cl_uint pi = 0; pi < nplatforms; pi++) {
        char pname[256] = {0};
        clGetPlatformInfo(platforms[pi], CL_PLATFORM_NAME, sizeof(pname), pname, nullptr);
        std::string pname_lower(pname);
        for (size_t i = 0; i < pname_lower.size(); i++) pname_lower[i] = tolower((unsigned char)pname_lower[i]);

        bool is_pocl = (pname_lower.find("pocl") != std::string::npos ||
                        pname_lower.find("portable computing") != std::string::npos);

        cl_uint ndevices = 0;
        if (clGetDeviceIDs(platforms[pi], CL_DEVICE_TYPE_GPU, 0, nullptr, &ndevices) != CL_SUCCESS) continue;

        std::cerr << "OpenCL platform: " << pname << " (" << ndevices << " GPU devices)" << std::flush;
        if (is_pocl) std::cerr << " [PoCL - CPU-based, skipped]" << std::flush;
        std::cerr << "\n" << std::flush;

        if (ndevices == 0) continue;

        std::vector<cl_device_id> devices(ndevices);
        clGetDeviceIDs(platforms[pi], CL_DEVICE_TYPE_GPU, ndevices, devices.data(), nullptr);

        for (cl_uint di = 0; di < ndevices; di++) {
            clGetDeviceInfo(devices[di], CL_DEVICE_NAME, sizeof(devname), devname, nullptr);
            std::cerr << "  device[" << di << "]: " << devname << "\n" << std::flush;
        }

        if (is_pocl) continue;

        if (chosenDevice == 0) {
            chosenPlatform = platforms[pi];
            chosenDevice = devices[0];
        }
    }

    if (chosenDevice == 0) {
        std::cerr << "no physical GPU platform found; falling back to CPU device\n" << std::flush;
        for (cl_uint pi = 0; pi < nplatforms; pi++) {
            if (clGetDeviceIDs(platforms[pi], CL_DEVICE_TYPE_CPU, 1, &chosenDevice, nullptr) == CL_SUCCESS
                && chosenDevice != 0) {
                chosenPlatform = platforms[pi];
                break;
            }
        }
        if (chosenDevice == 0) return false;
    }

    clGetDeviceInfo(chosenDevice, CL_DEVICE_NAME, sizeof(devname), devname, nullptr);
    std::cerr << "GPU: " << devname << "\n" << std::flush;

    g_ctx = clCreateContext(nullptr, 1, &chosenDevice, nullptr, nullptr, &err);
    if (err != CL_SUCCESS) {
        std::cerr << "clCreateContext failed: " << getCLError(err) << "\n";
        return false;
    }

    g_queue = clCreateCommandQueue(g_ctx, chosenDevice, 0, &err);
    if (err != CL_SUCCESS) {
        std::cerr << "clCreateCommandQueue failed: " << getCLError(err) << "\n";
        return false;
    }

    std::string kernel_src = readFile(kernelDir + "/kernel.cl");
    std::string combined = kernel_src;

    const char* src = combined.c_str();
    size_t src_len = combined.size();

    g_program = clCreateProgramWithSource(g_ctx, 1, &src, &src_len, &err);
    if (err != CL_SUCCESS) {
        std::cerr << "clCreateProgramWithSource failed: " << getCLError(err) << "\n";
        return false;
    }

    err = clBuildProgram(g_program, 1, &chosenDevice, "-cl-std=CL1.2 -cl-fast-relaxed-math", nullptr, nullptr);
    {
        size_t log_size = 0;
        clGetProgramBuildInfo(g_program, chosenDevice, CL_PROGRAM_BUILD_LOG, 0, nullptr, &log_size);
        if (log_size > 0 && log_size < 10000) {
            std::string build_log(log_size, 0);
            clGetProgramBuildInfo(g_program, chosenDevice, CL_PROGRAM_BUILD_LOG, log_size, &build_log[0], nullptr);
            std::cerr << "build log:\n" << build_log << "\n";
        }
    }
    if (err != CL_SUCCESS) {
        std::cerr << "clBuildProgram error: " << getCLError(err) << "\n";
        return false;
    }

    g_kernel = clCreateKernel(g_program, "search_nonce", &err);
    if (err != CL_SUCCESS) {
        std::cerr << "clCreateKernel failed: " << getCLError(err) << "\n";
        return false;
    }

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

/* ---- BLAKE3 block compression (host-side midstate) ----
 * Mirrors blake3_compress in kernel.cl.  The first two header blocks are
 * compressed once per work message to produce the chaining value (cv) the
 * kernel resumes from, so the device performs a single compression per nonce.
 */

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

// Compute the BLAKE3 midstate over the first two header blocks and the fixed
// block-2 words.  The nonce word (block2[3], bytes 140-143) is zeroed so the
// kernel injects the swept nonce.
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

bool search_gpu(const uint32_t* cv, const uint32_t* block2,
                const std::vector<uint8_t>& target,
                uint32_t start_nonce, uint32_t batch_size, uint32_t* found_nonce,
                std::vector<uint8_t>* found_hash) {
    cl_int err;

    cl_uint nonce_val = 0;

    cl_mem cv_buf = clCreateBuffer(g_ctx, CL_MEM_READ_ONLY | CL_MEM_COPY_HOST_PTR,
                                    32, (void*)cv, &err);
    if (err != CL_SUCCESS) return false;

    cl_mem b2_buf = clCreateBuffer(g_ctx, CL_MEM_READ_ONLY | CL_MEM_COPY_HOST_PTR,
                                    64, (void*)block2, &err);
    if (err != CL_SUCCESS) { clReleaseMemObject(cv_buf); return false; }

    cl_mem target_buf = clCreateBuffer(g_ctx, CL_MEM_READ_ONLY | CL_MEM_COPY_HOST_PTR,
                                        HASH_SIZE, (void*)target.data(), &err);
    if (err != CL_SUCCESS) {
        clReleaseMemObject(cv_buf);
        clReleaseMemObject(b2_buf);
        return false;
    }

    cl_mem result_buf = clCreateBuffer(g_ctx, CL_MEM_READ_WRITE | CL_MEM_COPY_HOST_PTR,
                                        sizeof(cl_uint), &nonce_val, &err);
    if (err != CL_SUCCESS) {
        clReleaseMemObject(cv_buf);
        clReleaseMemObject(b2_buf);
        clReleaseMemObject(target_buf);
        return false;
    }

    cl_mem hash_buf = clCreateBuffer(g_ctx, CL_MEM_WRITE_ONLY,
                                       HASH_SIZE, nullptr, &err);
    if (err != CL_SUCCESS) {
        clReleaseMemObject(cv_buf);
        clReleaseMemObject(b2_buf);
        clReleaseMemObject(target_buf);
        clReleaseMemObject(result_buf);
        return false;
    }

    clSetKernelArg(g_kernel, 0, sizeof(cl_mem), &cv_buf);
    clSetKernelArg(g_kernel, 1, sizeof(cl_mem), &b2_buf);
    clSetKernelArg(g_kernel, 2, sizeof(cl_mem), &target_buf);
    clSetKernelArg(g_kernel, 3, sizeof(cl_mem), &result_buf);
    clSetKernelArg(g_kernel, 4, sizeof(cl_mem), &hash_buf);
    clSetKernelArg(g_kernel, 5, sizeof(cl_uint), &start_nonce);
    clSetKernelArg(g_kernel, 6, sizeof(cl_uint), &batch_size);

    size_t global_size = batch_size;
    size_t local_size = 64;
    if (global_size < local_size) local_size = global_size;
    if (global_size == 0) global_size = local_size;

    cl_event evt = 0;
    fflush(stdout);
    int saved_stdout = dup(STDOUT_FILENO);
    int devnull = open("/dev/null", O_WRONLY);
    dup2(devnull, STDOUT_FILENO);
    close(devnull);
    err = clEnqueueNDRangeKernel(g_queue, g_kernel, 1, nullptr, &global_size, &local_size, 0, nullptr, &evt);
    dup2(saved_stdout, STDOUT_FILENO);
    close(saved_stdout);
    if (err != CL_SUCCESS) {
        std::cerr << "clEnqueueNDRangeKernel failed: " << getCLError(err) << "\n";
        clReleaseMemObject(cv_buf);
        clReleaseMemObject(b2_buf);
        clReleaseMemObject(target_buf);
        clReleaseMemObject(result_buf);
        clReleaseMemObject(hash_buf);
        return false;
    }

    if (evt != 0) {
        const cl_event wait_list = evt;
        clWaitForEvents(1, &wait_list);
        clReleaseEvent(evt);
    }

    clEnqueueReadBuffer(g_queue, result_buf, CL_TRUE, 0, sizeof(cl_uint), found_nonce, 0, nullptr, nullptr);

    if (*found_nonce != 0) {
        found_hash->resize(HASH_SIZE);
        clEnqueueReadBuffer(g_queue, hash_buf, CL_TRUE, 0, HASH_SIZE, found_hash->data(), 0, nullptr, nullptr);
    }

    clReleaseMemObject(cv_buf);
    clReleaseMemObject(b2_buf);
    clReleaseMemObject(target_buf);
    clReleaseMemObject(result_buf);
    clReleaseMemObject(hash_buf);

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
            // Wrapped past UINT32_MAX: the full 2^32 nonce space was searched.
            start_nonce = 1;
            break;
        }
        start_nonce = next_nonce;

        // Periodic progress so the miner can report hashrate.
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
        // Full 2^32 nonce sweep completed with no solution.
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

    if (!initOpenCL(kernelDir)) {
        std::cerr << "OpenCL init failed\n";
        return 1;
    }

    std::string line;
    while (std::getline(std::cin, line)) {
        if (!parseMessage(line)) break;
    }

    if (g_kernel) clReleaseKernel(g_kernel);
    if (g_program) clReleaseProgram(g_program);
    if (g_queue) clReleaseCommandQueue(g_queue);
    if (g_ctx) clReleaseContext(g_ctx);

    return 0;
}