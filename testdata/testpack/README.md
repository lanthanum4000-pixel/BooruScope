# Test pack (1,000 posts)

A small data set in the release format ([`docs/FORMAT.md`](../../docs/FORMAT.md)) used by the Go tests and published as the installer's trial pack.

- 1,000 yande.re posts rated safe, gathered by searching the full index with 77 seed images and collecting results rank by rank until 1,000 unique posts were reached.
- Same inclusion rules as the full index: only posts rated safe, without explicit tags or unreadable tags.
- No images are included, only post IDs and MD5 hashes.

## `expected.json`

`queries` holds 77 entries:

- `vector_from`: `index` (vector taken from the full index) or `encoded` (computed with the production preprocessing chain).
- `query_vec_f32_le_b64`: the query vector, 768 little-endian f32 values, base64.
- `exact`, `int8`, `none_m64`, `none_m96`, `none_m128`: top 20 within this pack, each with `row`, `post_id`, `score`. `exact` is the cosine ranking on the full-precision vectors (not included in the pack); `int8` is a full int8 scan; `none_m*` is the coarse PQ ranking.

An implementation fed `query_vec_f32_le_b64` must reproduce `int8` and `none_m*` rank for rank, with scores within 1e-4.

Mean top-10 overlap with `exact`: int8 0.961, M64 0.775, M96 0.801, M128 0.832.
