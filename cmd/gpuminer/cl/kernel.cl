#ifdef cl_khr_global_int32_base_atomics
#pragma OPENCL EXTENSION cl_khr_global_int32_base_atomics : enable
#endif

typedef uint uint32_t;

constant uint32_t IV[8] = {
    0x6A09E667U, 0xBB67AE85U, 0x3C6EF372U, 0xA54FF53AU,
    0x510E527FU, 0x9B05688CU, 0x1F83D9ABU, 0x5BE0CD19U
};

static inline uint32_t ROTR32(uint32_t x, int n) {
    return (x >> n) | (x << (32 - n));
}

static inline void G(uint32_t* v, int a, int b, int c, int d, uint32_t x, uint32_t y) {
    v[a] = v[a] + v[b] + x;
    v[d] = ROTR32(v[d] ^ v[a], 16);
    v[c] = v[c] + v[d];
    v[b] = ROTR32(v[b] ^ v[c], 12);
    v[a] = v[a] + v[b] + y;
    v[d] = ROTR32(v[d] ^ v[a], 8);
    v[c] = v[c] + v[d];
    v[b] = ROTR32(v[b] ^ v[c], 7);
}

static inline void blake3_compress(uint32_t* m, __constant const uint32_t* cv,
                                   uint32_t counter, uint32_t block_len,
                                   uint32_t flags, uint32_t* out) {
    uint32_t v[16];
    v[ 0] = cv[0]; v[ 1] = cv[1]; v[ 2] = cv[2]; v[ 3] = cv[3];
    v[ 4] = cv[4]; v[ 5] = cv[5]; v[ 6] = cv[6]; v[ 7] = cv[7];
    v[ 8] = IV[0]; v[ 9] = IV[1]; v[10] = IV[2]; v[11] = IV[3];
    v[12] = counter; v[13] = 0; v[14] = block_len; v[15] = flags;

    /* Round 0 */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);
    { uint32_t t[16]; for(int i=0;i<16;i++) t[i]=m[i];
      m[0]=t[2];m[1]=t[6];m[2]=t[3];m[3]=t[10];m[4]=t[7];m[5]=t[0];
      m[6]=t[4];m[7]=t[13];m[8]=t[1];m[9]=t[11];m[10]=t[12];m[11]=t[5];
      m[12]=t[9];m[13]=t[14];m[14]=t[15];m[15]=t[8]; }

    /* Round 1 */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);
    { uint32_t t[16]; for(int i=0;i<16;i++) t[i]=m[i];
      m[0]=t[2];m[1]=t[6];m[2]=t[3];m[3]=t[10];m[4]=t[7];m[5]=t[0];
      m[6]=t[4];m[7]=t[13];m[8]=t[1];m[9]=t[11];m[10]=t[12];m[11]=t[5];
      m[12]=t[9];m[13]=t[14];m[14]=t[15];m[15]=t[8]; }

    /* Round 2 */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);
    { uint32_t t[16]; for(int i=0;i<16;i++) t[i]=m[i];
      m[0]=t[2];m[1]=t[6];m[2]=t[3];m[3]=t[10];m[4]=t[7];m[5]=t[0];
      m[6]=t[4];m[7]=t[13];m[8]=t[1];m[9]=t[11];m[10]=t[12];m[11]=t[5];
      m[12]=t[9];m[13]=t[14];m[14]=t[15];m[15]=t[8]; }

    /* Round 3 */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);
    { uint32_t t[16]; for(int i=0;i<16;i++) t[i]=m[i];
      m[0]=t[2];m[1]=t[6];m[2]=t[3];m[3]=t[10];m[4]=t[7];m[5]=t[0];
      m[6]=t[4];m[7]=t[13];m[8]=t[1];m[9]=t[11];m[10]=t[12];m[11]=t[5];
      m[12]=t[9];m[13]=t[14];m[14]=t[15];m[15]=t[8]; }

    /* Round 4 */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);
    { uint32_t t[16]; for(int i=0;i<16;i++) t[i]=m[i];
      m[0]=t[2];m[1]=t[6];m[2]=t[3];m[3]=t[10];m[4]=t[7];m[5]=t[0];
      m[6]=t[4];m[7]=t[13];m[8]=t[1];m[9]=t[11];m[10]=t[12];m[11]=t[5];
      m[12]=t[9];m[13]=t[14];m[14]=t[15];m[15]=t[8]; }

    /* Round 5 */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);
    { uint32_t t[16]; for(int i=0;i<16;i++) t[i]=m[i];
      m[0]=t[2];m[1]=t[6];m[2]=t[3];m[3]=t[10];m[4]=t[7];m[5]=t[0];
      m[6]=t[4];m[7]=t[13];m[8]=t[1];m[9]=t[11];m[10]=t[12];m[11]=t[5];
      m[12]=t[9];m[13]=t[14];m[14]=t[15];m[15]=t[8]; }

    /* Round 6 (last — no permutation after) */
    G(v,0,4,8,12, m[0],m[1]); G(v,1,5,9,13, m[2],m[3]);
    G(v,2,6,10,14, m[4],m[5]); G(v,3,7,11,15, m[6],m[7]);
    G(v,0,5,10,15, m[8],m[9]); G(v,1,6,11,12, m[10],m[11]);
    G(v,2,7,8,13, m[12],m[13]); G(v,3,4,9,14, m[14],m[15]);

    for (int i = 0; i < 8; i++) {
        v[i]     ^= v[i + 8];
        v[i + 8] ^= cv[i];
    }
    for (int i = 0; i < 8; i++) out[i] = v[i];
}

/* search_nonce resumes from a precomputed BLAKE3 midstate.
 *
 * The first 128 bytes of the header never change while sweeping the nonce, so
 * the host compresses blocks 0 and 1 once per work message and hands the device
 * the resulting chaining value (cv) plus the fixed block-2 words.  The device
 * performs a single compression per nonce by injecting the nonce into block-2
 * word 3 (bytes 140-143).
 *
 * cv[8]     — chaining value after blocks 0 and 1.
 * block2[16]— block 2 words (bytes 128-191); word 3 is the nonce slot and is 0.
 */
__attribute__((reqd_work_group_size(64, 1, 1)))
__kernel void search_nonce(
    __constant const uint32_t* cv,
    __constant const uint32_t* block2,
    __global const uchar* target,
    __global volatile int* result,
    __global uchar* hash_out,
    uint start_nonce,
    uint nonces_to_search)
{
    uint gid = get_global_id(0);
    if (gid >= nonces_to_search) return;

    uint nonce = start_nonce + gid;
    if (nonce == 0) return;

    /* Block 2 (bytes 128-191): copy fixed words, inject the nonce, zero-pad */
    uint32_t m2[16];
    for (int i = 0; i < 16; i++) m2[i] = block2[i];
    m2[3] = nonce;

    uint32_t tmp[8];
    blake3_compress(m2, cv, 0, 52, 0x02 | 0x08, tmp);

    /* Compare hash (uint256 LE) <= target */
    uint32_t tgt[8];
    for (int i = 0; i < 8; i++)
        tgt[i] = ((__global const uint32_t*)target)[i];

    int accept = 1;
    for (int i = 7; i >= 0; i--) {
        if (tmp[i] > tgt[i]) { accept = 0; break; }
        if (tmp[i] < tgt[i]) break;
    }

    if (accept) {
        int old = atom_cmpxchg(result, 0, (int)nonce);
        if (old == 0) {
            for (int i = 0; i < 8; i++)
                ((__global uint32_t*)hash_out)[i] = tmp[i];
        }
    }
}
