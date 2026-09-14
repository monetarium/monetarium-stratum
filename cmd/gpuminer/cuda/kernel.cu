typedef unsigned int u32;

__constant__ u32 d_cv[8];
__constant__ u32 d_block2[16];
__constant__ u32 d_target[8];

__device__ __forceinline__ u32 rotr32(u32 x, int n) {
    return (x >> n) | (x << (32 - n));
}

__device__ __forceinline__ void b3_g(u32* v, int a, int b, int c, int d,
                                     u32 x, u32 y) {
    v[a] = v[a] + v[b] + x;
    v[d] = rotr32(v[d] ^ v[a], 16);
    v[c] = v[c] + v[d];
    v[b] = rotr32(v[b] ^ v[c], 12);
    v[a] = v[a] + v[b] + y;
    v[d] = rotr32(v[d] ^ v[a], 8);
    v[c] = v[c] + v[d];
    v[b] = rotr32(v[b] ^ v[c], 7);
}

__device__ __forceinline__ void blake3_compress(const u32* m, const u32* cv,
                                                u32 counter, u32 block_len,
                                                u32 flags, u32* out) {
    u32 v[16];
    v[ 0] = cv[0]; v[ 1] = cv[1]; v[ 2] = cv[2]; v[ 3] = cv[3];
    v[ 4] = cv[4]; v[ 5] = cv[5]; v[ 6] = cv[6]; v[ 7] = cv[7];
    v[ 8] = 0x6A09E667U; v[ 9] = 0xBB67AE85U; v[10] = 0x3C6EF372U; v[11] = 0xA54FF53AU;
    v[12] = 0x510E527FU; v[13] = 0x9B05688CU; v[14] = 0x1F83D9ABU; v[15] = 0x5BE0CD19U;
    v[12] = counter; v[13] = 0; v[14] = block_len; v[15] = flags;

    u32 w[16], t[16];
    for (int i = 0; i < 16; i++) w[i] = m[i];

    for (int r = 0; r < 7; r++) {
        b3_g(v,0,4,8,12, w[0],w[1]); b3_g(v,1,5,9,13, w[2],w[3]);
        b3_g(v,2,6,10,14, w[4],w[5]); b3_g(v,3,7,11,15, w[6],w[7]);
        b3_g(v,0,5,10,15, w[8],w[9]); b3_g(v,1,6,11,12, w[10],w[11]);
        b3_g(v,2,7,8,13, w[12],w[13]); b3_g(v,3,4,9,14, w[14],w[15]);
        if (r == 6) break;
        for (int i = 0; i < 16; i++) t[i] = w[i];
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

__global__ void search_nonce(u32 start_nonce, u32 nonces_to_search,
                             int* result, u32* hash_out) {
    u32 gid = blockIdx.x * blockDim.x + threadIdx.x;
    if (gid >= nonces_to_search) return;

    u32 nonce = start_nonce + gid;
    if (nonce == 0) return;

    u32 m2[16];
    for (int i = 0; i < 16; i++) m2[i] = d_block2[i];
    m2[3] = nonce;

    u32 tmp[8];
    blake3_compress(m2, d_cv, 0, 52, 0x02 | 0x08, tmp);

    int accept = 1;
    for (int i = 7; i >= 0; i--) {
        if (tmp[i] > d_target[i]) { accept = 0; break; }
        if (tmp[i] < d_target[i]) break;
    }

    if (accept) {
        int old = atomicCAS(result, 0, (int)nonce);
        if (old == 0) {
            for (int i = 0; i < 8; i++)
                hash_out[i] = tmp[i];
        }
    }
}