#include "cpu_reference.h"

#include <pthread.h>
#include <string.h>
#include <unistd.h>

#include "cpu_reference.h"

#define HEADER_SIZE 180

static const uint32_t B3_IV[8] = {
    0x6A09E667U, 0xBB67AE85U, 0x3C6EF372U, 0xA54FF53AU,
    0x510E527FU, 0x9B05688CU, 0x1F83D9ABU, 0x5BE0CD19U
};

static uint32_t rotr32(uint32_t x, int n) {
    return (x >> n) | (x << (32 - n));
}

static void b3_g(uint32_t* v, int a, int b, int c, int d, uint32_t x, uint32_t y) {
    v[a] = v[a] + v[b] + x;
    v[d] = rotr32(v[d] ^ v[a], 16);
    v[c] = v[c] + v[d];
    v[b] = rotr32(v[b] ^ v[c], 12);
    v[a] = v[a] + v[b] + y;
    v[d] = rotr32(v[d] ^ v[a], 8);
    v[c] = v[c] + v[d];
    v[b] = rotr32(v[b] ^ v[c], 7);
}

/* Identical to blake3_compress in cuda/kernel.cu and cl/kernel.cl. */
void cpu_blake3_compress(const uint32_t m[16], const uint32_t cv[8],
                         uint32_t counter, uint32_t block_len, uint32_t flags,
                         uint32_t out[8]) {
    uint32_t v[16];
    uint32_t w[16], t[16];

    v[0] = cv[0]; v[1] = cv[1]; v[2] = cv[2]; v[3] = cv[3];
    v[4] = cv[4]; v[5] = cv[5]; v[6] = cv[6]; v[7] = cv[7];
    v[8] = B3_IV[0]; v[9] = B3_IV[1]; v[10] = B3_IV[2]; v[11] = B3_IV[3];
    v[12] = counter; v[13] = 0; v[14] = block_len; v[15] = flags;

    memcpy(w, m, sizeof(w));

    for (int r = 0; r < 7; r++) {
        b3_g(v, 0, 4, 8, 12, w[0], w[1]);
        b3_g(v, 1, 5, 9, 13, w[2], w[3]);
        b3_g(v, 2, 6, 10, 14, w[4], w[5]);
        b3_g(v, 3, 7, 11, 15, w[6], w[7]);
        b3_g(v, 0, 5, 10, 15, w[8], w[9]);
        b3_g(v, 1, 6, 11, 12, w[10], w[11]);
        b3_g(v, 2, 7, 8, 13, w[12], w[13]);
        b3_g(v, 3, 4, 9, 14, w[14], w[15]);
        if (r == 6) break;
        memcpy(t, w, sizeof(t));
        w[0] = t[2]; w[1] = t[6]; w[2] = t[3]; w[3] = t[10];
        w[4] = t[7]; w[5] = t[0]; w[6] = t[4]; w[7] = t[13];
        w[8] = t[1]; w[9] = t[11]; w[10] = t[12]; w[11] = t[5];
        w[12] = t[9]; w[13] = t[14]; w[14] = t[15]; w[15] = t[8];
    }

    for (int i = 0; i < 8; i++) {
        v[i] ^= v[i + 8];
        v[i + 8] ^= cv[i];
    }
    for (int i = 0; i < 8; i++) out[i] = v[i];
}

void cpu_compute_midstate(const uint8_t header[180], uint32_t cv[8],
                          uint32_t block2[16]) {
    uint32_t m[16], tmp[8];

    for (int i = 0; i < 8; i++) cv[i] = B3_IV[i];

    for (int i = 0; i < 16; i++) m[i] = (uint32_t)(unsigned char)header[i*4]
        | (uint32_t)(((unsigned char)header[i*4+1]) << 8)
        | (uint32_t)(((unsigned char)header[i*4+2]) << 16)
        | (uint32_t)(((unsigned char)header[i*4+3]) << 24);
    cpu_blake3_compress(m, cv, 0, 64, 0x01, tmp);
    memcpy(cv, tmp, 32);

    for (int i = 0; i < 16; i++) m[i] = (uint32_t)(unsigned char)header[64+i*4]
        | (uint32_t)(((unsigned char)header[64+i*4+1]) << 8)
        | (uint32_t)(((unsigned char)header[64+i*4+2]) << 16)
        | (uint32_t)(((unsigned char)header[64+i*4+3]) << 24);
    cpu_blake3_compress(m, cv, 0, 64, 0x00, tmp);
    memcpy(cv, tmp, 32);

    memset(block2, 0, 64);
    for (int i = 0; i < 13; i++) block2[i] = (uint32_t)(unsigned char)header[128+i*4]
        | (uint32_t)(((unsigned char)header[128+i*4+1]) << 8)
        | (uint32_t)(((unsigned char)header[128+i*4+2]) << 16)
        | (uint32_t)(((unsigned char)header[128+i*4+3]) << 24);
    block2[3] = 0;
}

/* Returns 1 when the hash words satisfy hash <= target (word compare 7..0). */
static int accepts(const uint32_t tmp[8], const uint32_t target[8]) {
    int accept = 1;
    for (int i = 7; i >= 0; i--) {
        if (tmp[i] > target[i]) { accept = 0; break; }
        if (tmp[i] < target[i]) break;
    }
    return accept;
}

void cpu_search_nonce(const uint32_t cv[8], const uint32_t block2[16],
                      const uint32_t target[8], uint32_t start_nonce,
                      uint32_t nonces_to_search, int32_t* found,
                      uint32_t hash_out[8]) {
    uint32_t gid;
    for (gid = 0; gid < nonces_to_search; gid++) {
        uint32_t nonce = start_nonce + gid;
        uint32_t m2[16], tmp[8];
        if (nonce == 0) continue;
        memcpy(m2, block2, sizeof(m2));
        m2[3] = nonce;
        cpu_blake3_compress(m2, cv, 0, 52, 0x02 | 0x08, tmp);
        if (accepts(tmp, target)) {
            *found = (int32_t)nonce;
            if (hash_out) memcpy(hash_out, tmp, 32);
            return;
        }
    }
    *found = 0;
}

struct search_ctx {
    const uint32_t* cv;
    const uint32_t* block2;
    const uint32_t* target;
    uint32_t start_nonce;
    uint32_t nonces_to_search;
    uint32_t slice;
    uint32_t slices;
    int32_t found;
    uint32_t hash_out[8];
};

static void* search_slice(void* arg) {
    struct search_ctx* c = (struct search_ctx*)arg;
    uint32_t per = (c->nonces_to_search + c->slices - 1) / c->slices;
    uint32_t begin = c->slice * per;
    uint32_t end = begin + per;
    if (end > c->nonces_to_search || c->slice == c->slices - 1)
        end = c->nonces_to_search;
    c->found = 0;
    for (uint32_t gid = begin; gid < end; gid++) {
        uint32_t nonce = c->start_nonce + gid;
        uint32_t m2[16], tmp[8];
        if (nonce == 0) continue;
        memcpy(m2, c->block2, sizeof(m2));
        m2[3] = nonce;
        cpu_blake3_compress(m2, c->cv, 0, 52, 0x02 | 0x08, tmp);
        if (accepts(tmp, c->target)) {
            c->found = (int32_t)nonce;
            memcpy(c->hash_out, tmp, 32);
            return NULL;
        }
    }
    return NULL;
}

/*
 * Parallel deterministic search.  Each thread scans a disjoint range of gids in
 * ascending order; the winning nonce is the minimum found across slices, which
 * equals the lowest matching nonce overall -- deterministic for any thread
 * count.  Hashing of the winner is redone once globally so no per-thread state
 * leaks into the result.
 */
void cpu_search_nonce_parallel(const uint32_t cv[8], const uint32_t block2[16],
                               const uint32_t target[8], uint32_t start_nonce,
                               uint32_t nonces_to_search, int32_t* found,
                               uint32_t hash_out[8]) {
    unsigned int hw = 0;
#if defined(_SC_NPROCESSORS_ONLN)
    hw = (unsigned int)sysconf(_SC_NPROCESSORS_ONLN);
#endif
    if (hw == 0 || hw > 64) hw = 1;
    if (nonces_to_search < hw * 4 || nonces_to_search <= 4096) {
        cpu_search_nonce(cv, block2, target, start_nonce, nonces_to_search,
                         found, hash_out);
        return;
    }

    struct search_ctx ctx[64];
    pthread_t threads[64];
    unsigned int n = (nonces_to_search + (hw - 1)) / hw < 1 ? 1 : hw;

    int32_t winner = 0;
    for (unsigned int s = 0; s < n; s++) {
        ctx[s].cv = cv;
        ctx[s].block2 = block2;
        ctx[s].target = target;
        ctx[s].start_nonce = start_nonce;
        ctx[s].nonces_to_search = nonces_to_search;
        ctx[s].slice = s;
        ctx[s].slices = n;
        ctx[s].found = 0;
        if (pthread_create(&threads[s], NULL, search_slice, &ctx[s]) != 0) {
            /* fall back to the sequential path on thread-creation failure */
            cpu_search_nonce(cv, block2, target, start_nonce,
                             nonces_to_search, found, hash_out);
            for (unsigned int j = 0; j < s; j++) pthread_join(threads[j], NULL);
            return;
        }
    }
    for (unsigned int s = 0; s < n; s++)
        pthread_join(threads[s], NULL);

    for (unsigned int s = 0; s < n; s++) {
        int32_t f = ctx[s].found;
        if (f == 0) continue;
        if (winner == 0 || (uint32_t)f < (uint32_t)winner) {
            winner = f;
            memcpy(hash_out, ctx[s].hash_out, 32);
        }
    }
    *found = winner;
}