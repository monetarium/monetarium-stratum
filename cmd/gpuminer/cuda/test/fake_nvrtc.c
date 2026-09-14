#include "fake_nvrtc.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* ------------------------------------------------------------------ */
/* Program registry                                                     */
/* ------------------------------------------------------------------ */

struct prog {
    int   id;
    int   alive;
    char  name[256];
    char  source[8192];
    char  ptx[4096];
    char  log[1024];
    /* lowered names for the expressions the host registers */
    char  lowered_search[64];
    char  lowered_cv[64];
    char  lowered_block2[64];
    char  lowered_target[64];
    int   compiled;
};

#define MAX_PROGS 8
static struct prog progs[MAX_PROGS];
static int         prog_count;

static struct prog* find_prog(nvrtcProgram p) {
    int id = (int)(uintptr_t)p;
    for (int i = 0; i < prog_count; i++)
        if (progs[i].id == id && progs[i].alive) return &progs[i];
    return NULL;
}

/* ------------------------------------------------------------------ */
/* Fail-next hook                                                      */
/* ------------------------------------------------------------------ */

static int fail_next_op  = -1;
static int fail_next_err = 0;

void fake_nvrtc_fail_next(int op, int err) {
    fail_next_op  = op;
    fail_next_err = err;
}

static nvrtcResult check_fail(void) {
    if (fail_next_op >= 0) {
        nvrtcResult err = (nvrtcResult)fail_next_err;
        fail_next_op  = -1;
        fail_next_err = 0;
        return err;
    }
    return NVRTC_SUCCESS;
}

/* ------------------------------------------------------------------ */
/* NVRTC API                                                           */
/* ------------------------------------------------------------------ */

nvrtcResult nvrtcCreateProgram(nvrtcProgram* program, const char* src,
                               const char* name, int num_headers,
                               const char** headers, const char** include_names) {
    (void)num_headers; (void)headers; (void)include_names;
    nvrtcResult r = check_fail();
    if (r) return r;
    if (!program || !src) return NVRTC_ERROR_INVALID_INPUT;
    if (prog_count >= MAX_PROGS) return NVRTC_ERROR_INVALID_INPUT;
    struct prog* p = &progs[prog_count];
    p->id = prog_count + 1;
    p->alive = 1;
    strncpy(p->source, src, sizeof(p->source));
    p->source[sizeof(p->source) - 1] = '\0';
    if (name) {
        strncpy(p->name, name, sizeof(p->name));
        p->name[sizeof(p->name) - 1] = '\0';
    } else {
        p->name[0] = '\0';
    }
    p->ptx[0] = '\0';
    p->log[0] = '\0';
    p->lowered_search[0] = '\0';
    p->lowered_cv[0] = '\0';
    p->lowered_block2[0] = '\0';
    p->lowered_target[0] = '\0';
    p->compiled = 0;
    prog_count++;
    *program = (nvrtcProgram)(uintptr_t)p->id;
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcAddNameExpression(nvrtcProgram program,
                                   const char* name_expression) {
    nvrtcResult r = check_fail();
    if (r) return r;
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    if (!name_expression) return NVRTC_ERROR_INVALID_INPUT;
    /*
     * The host registers the kernel name and the three __constant__ globals
     * (in "&d_cv" address-of form) and expects deterministic lowered names so
     * cuModuleGetFunction / cuModuleGetGlobal can look them up.
     */
    if (strcmp(name_expression, "search_nonce") == 0)
        strncpy(p->lowered_search, "search_nonce", sizeof(p->lowered_search));
    else if (strcmp(name_expression, "&d_cv") == 0)
        strncpy(p->lowered_cv, "d_cv", sizeof(p->lowered_cv));
    else if (strcmp(name_expression, "&d_block2") == 0)
        strncpy(p->lowered_block2, "d_block2", sizeof(p->lowered_block2));
    else if (strcmp(name_expression, "&d_target") == 0)
        strncpy(p->lowered_target, "d_target", sizeof(p->lowered_target));
    /* registered even if ignored */
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcGetLoweredName(nvrtcProgram program,
                                const char* expression, const char** lowered) {
    nvrtcResult r = check_fail();
    if (r) return r;
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    if (!lowered) return NVRTC_ERROR_INVALID_INPUT;
    if (strcmp(expression, "search_nonce") == 0) {
        *lowered = p->lowered_search;
        return NVRTC_SUCCESS;
    }
    if (strcmp(expression, "&d_cv") == 0) {
        *lowered = p->lowered_cv;
        return NVRTC_SUCCESS;
    }
    if (strcmp(expression, "&d_block2") == 0) {
        *lowered = p->lowered_block2;
        return NVRTC_SUCCESS;
    }
    if (strcmp(expression, "&d_target") == 0) {
        *lowered = p->lowered_target;
        return NVRTC_SUCCESS;
    }
    return NVRTC_ERROR_NAME_NOT_EXPRESSED;
}

nvrtcResult nvrtcCompileProgram(nvrtcProgram program, int num_options,
                                const char** options) {
    nvrtcResult r = check_fail();
    if (r == NVRTC_SUCCESS) {
        /* FAKE_NVRTC_FAIL_COMPILE=1 lets the Go integration tests force a
         * compile failure through the unmodified cuda_host binary. */
        const char* fail = getenv("FAKE_NVRTC_FAIL_COMPILE");
        if (fail && *fail == '1') r = NVRTC_ERROR_COMPILATION;
    }
    if (r) {
        struct prog* p = find_prog(program);
        if (p) {
            strncpy(p->log, "fake nvrtc: compilation failed",
                    sizeof(p->log) - 1);
            p->compiled = 0;
        }
        return r;
    }
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;

    /* decide compute capability from options: "--gpu-architecture=compute_X" */
    int cc = 89; /* default 8.9 (fake device) */
    for (int i = 0; i < num_options; i++) {
        if (!options[i]) continue;
        if (strncmp(options[i], "--gpu-architecture=compute_", 27) == 0) {
            const char* v = options[i] + 27;
            int val = atoi(v);
            if (val >= 50) cc = val;
        }
    }
    /*
     * The host happily accepts whatever PTX we produce for any of the
     * compute_{89,60,50} options it passes, so we always succeed with a
     * deterministic stub and record the arch the call was made with.
     */
    (void)cc;
    snprintf(p->ptx, sizeof(p->ptx),
             "// fake nvrtc ptx v1\n"
             ".visible .entry search_nonce()\n"
             "{\n}"
             "\n");
    p->log[0] = '\0';
    p->compiled = 1;
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcGetPTXSize(nvrtcProgram program, size_t* size) {
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    if (!size) return NVRTC_ERROR_INVALID_INPUT;
    if (!p->compiled) return NVRTC_ERROR_INVALID_INPUT;
    *size = strlen(p->ptx) + 1;
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcGetPTX(nvrtcProgram program, char* ptx) {
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    if (!ptx) return NVRTC_ERROR_INVALID_INPUT;
    if (!p->compiled) return NVRTC_ERROR_INVALID_INPUT;
    strcpy(ptx, p->ptx);
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcGetProgramLogSize(nvrtcProgram program, size_t* size) {
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    if (!size) return NVRTC_ERROR_INVALID_INPUT;
    *size = strlen(p->log) + 1;
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcGetProgramLog(nvrtcProgram program, char* log) {
    struct prog* p = find_prog(program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    if (!log) return NVRTC_ERROR_INVALID_INPUT;
    strcpy(log, p->log);
    return NVRTC_SUCCESS;
}

nvrtcResult nvrtcDestroyProgram(nvrtcProgram* program) {
    nvrtcResult r = check_fail();
    if (r) return r;
    if (!program) return NVRTC_ERROR_INVALID_INPUT;
    struct prog* p = find_prog(*program);
    if (!p) return NVRTC_ERROR_INVALID_INPUT;
    p->alive = 0;
    *program = NULL;
    return NVRTC_SUCCESS;
}

const char* nvrtcGetErrorString(nvrtcResult result) {
    switch (result) {
        case NVRTC_SUCCESS:               return "NVRTC_SUCCESS";
        case NVRTC_ERROR_COMPILATION:     return "NVRTC_ERROR_COMPILATION";
        case NVRTC_ERROR_NAME_NOT_EXPRESSED: return "NVRTC_ERROR_NAME_NOT_EXPRESSED";
        default:                          return "NVRTC_ERROR_INTERNAL_ERROR";
    }
}