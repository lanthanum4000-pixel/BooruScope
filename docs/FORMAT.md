# Data file format (v1)

All integers and floats are little-endian. Row `i` in every file refers to the same post, the `i`-th record of `posts.bin`.

## `posts.bin`

| Field | Type |
|---|---|
| magic | 8 bytes `BSPOSTS1` |
| n | u32 |
| rows | n × (post_id u32, md5 16 raw bytes) |

The MD5 is always 16 raw bytes; it can end in `0x00`, so never read it as a string. The thumbnail URL is `https://assets.yande.re/data/preview/<md5[0:2]>/<md5[2:4]>/<md5>.jpg` with the MD5 in lowercase hex, and the post page is `https://yande.re/post/show/<post_id>`.

## `index_m64.pq` / `index_m96.pq` / `index_m128.pq`

| Field | Type |
|---|---|
| magic | 8 bytes `BSPQ0001` |
| m, dim, n, k | u32 × 4 (dim = 768, k = 256) |
| mean | f32[dim] |
| R | f32[dim × dim], row-major: `out[j] = Σ_i x[i] · R[i·dim + j]` |
| centroids | f32[m × k × (dim/m)], by segment, then centroid, then component |
| codes | u8[n × m] |

The rotation is a PCA basis trained on a 200,000-post sample; eigenvectors are dealt to the `m` segments in snake order by eigenvalue. Each segment has 256 k-means centroids.

## `rerank_int8.bin`

| Field | Type |
|---|---|
| magic | 8 bytes `BSI8V001` |
| n, dim | u32 × 2 |
| scale | f32[dim] |
| rows | i8[n × dim], `round(v / scale)` clamped to ±127 |

`v` is the unit-length CLIP vector. Every vector was computed from the post's yande.re preview (the image at the thumbnail URL above), not from the sample or the original file. `scale[j]` is the 99.99th percentile of `|v_j|` over a sample, divided by 127.

## Scoring

- Coarse: `qr = q·R` (no mean subtraction); `table[s][c] = qr_s · centroids[s][c]`; `score_i = Σ_s table[s][codes[i][s]] + q·mean`.
- int8: `score_i = Σ_j rows[i][j] · scale[j] · q[j]`.
