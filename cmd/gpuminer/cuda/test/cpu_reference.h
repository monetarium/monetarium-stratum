#ifndef CPU_REFERENCE_H
#define CPU_REFERENCE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/*
 * CPU reference implementation of the mining operation performed by the CUDA
 * kernel cuda/kernel.cu.  It is a native, deterministic translation of the
 * underlying algorithm -- not an emulation of CUDA threads/blocks.
 */

/*
 * BLAKE3 compression of one 64-byte block against a chaining value.  Mirrors
 * blake3_compress in cuda/kernel.cu and cl/kernel.cl and b3_compress in
 * cuda/host.cpp.  out receives 8 words (32 bytes).
 */
void cpu_blake3_compress(const uint32_t m[16], const uint32_t cv[8],
                         uint32_t counter, uint32_t block_len, uint32_t flags,
                         uint32_t out[8]);

/*
 * Computes the midstate the host passes to the kernel: compresses header
 * blocks 0 and 1 once into cv, then formats the fixed block 2 words with the
 * nonce slot (word 3) zeroed.  header has HEADER_SIZE (180) bytes.
 */
void cpu_compute_midstate(const uint8_t header[180], uint32_t cv[8],
                          uint32_t block2[16]);

/*
 * Sequential scan over gids [0, nonces_to_search) mirroring the CUDA
 * search_nonce kernel exactly:
 *
 *   nonce = start_nonce + gid          (nonce 0 is skipped)
 *   m2 = block2 with m2[3] = nonce
 *   tmp = compress(m2, cv, 0, 52, 0x02|0x08)
 *   accept if tmp <= target (256-bit word comparison from word 7 down)
 *
 * On the first match (lowest nonce, since the scan is ascending) *found is set
 * to the nonce and hash_out receives the 8 matching hash words.  If no match
 * exists *found stays 0.  This mirrors the kernel's atomicCAS + hash_out write
 * with deterministic, iteration-ordered result selection.
 */
void cpu_search_nonce(const uint32_t cv[8], const uint32_t block2[16],
                      const uint32_t target[8], uint32_t start_nonce,
                      uint32_t nonces_to_search, int32_t* found,
                      uint32_t hash_out[8]);

/*
 * Parallel variant of cpu_search_nonce for large ranges.  Deterministic: the
 * winner is always the lowest matching nonce regardless of thread count.
 */
void cpu_search_nonce_parallel(const uint32_t cv[8], const uint32_t block2[16],
                               const uint32_t target[8], uint32_t start_nonce,
                               uint32_t nonces_to_search, int32_t* found,
                               uint32_t hash_out[8]);

#ifdef __cplusplus
}
#endif

#endif /* CPU_REFERENCE_H */