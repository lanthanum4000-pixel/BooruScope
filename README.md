# BooruScope

Find the booru post behind a low-resolution image. Drop a picture into BooruScope, get a wall of visually similar Gelbooru posts, and double-click a card to open that post in your browser.

BooruScope runs entirely on your computer. It embeds the dropped image with CLIP ViT-L/14, searches a downloaded product-quantized (PQ) index of Gelbooru posts, and can refine the ranking with an optional int8 vector file.

> BooruScope is an independent project. It is not affiliated with or endorsed by Gelbooru.

## How it works

```
Go app ── local HTTP on 127.0.0.1 ── app window (Chrome or Edge in app mode)
   │
   ├─ PQ search: index in memory → coarse ranking → optional int8 rerank from disk
   ├─ thumbnail proxy (memory cache only)
   └─ Python child process: image → preprocessing chain → CLIP image embedding (768-d, unit length)
```

- The window is Chrome in `--app` mode (Edge if Chrome is not installed). If neither is found the default browser opens the same page.
- On first start BooruScope creates its own Python virtual environment and installs PyTorch and Hugging Face Transformers into it. The CLIP weights are then downloaded from Hugging Face (about 1.7 GB). Nothing is installed outside the BooruScope folder.

## Requirements

- Windows, macOS or Linux
- Python 3.9 or newer on `PATH` (only used once, to create the virtual environment)
- Google Chrome or Microsoft Edge
- RAM: about 1.5 GB for the encoder plus the index you pick (see below)
- An NVIDIA GPU is optional. With `nvidia-smi` present the CUDA build of PyTorch is installed; otherwise the CPU build.

## Data files

The data files are published at [huggingface.co/Lanthanum4000/BooruScope](https://huggingface.co/Lanthanum4000/BooruScope). The installer downloads them for you; to install by hand, put `posts.bin`, one index file and, if you want reranking, `rerank_int8.bin` into `<home>/data` (or point `-data` at another folder):

| File | Needed | Size (9.18 M posts) | Content |
|---|---|---|---|
| `posts.bin` | yes | ~0.18 GB | post ID and MD5 for every row |
| `index_m64.pq` / `index_m96.pq` / `index_m128.pq` | at least one | ~0.59 / 0.88 / 1.18 GB | rotation, codebooks and PQ codes (kept in memory) |
| `rerank_int8.bin` | optional | ~7.1 GB | int8 vectors for reranking (read from disk per query) |

The file layout is documented in [`docs/FORMAT.md`](docs/FORMAT.md). A 1,000-post sample in the same format lives in [`testdata/testpack`](testdata/testpack).

Measured quality on 50 random Gelbooru queries (ground truth: exact cosine ranking on the full-precision vectors):

| Index | Memory | Coarse only: top-10 overlap / true #1 in top 10 | With int8 rerank: top-10 overlap / same #1 |
|---|---|---|---|
| M64 | 630 MB | 0.52 / 0.70 | 0.94 / 0.90 |
| M96 | 910 MB | 0.64 / 0.92 | 0.94 / 0.90 |
| M128 | 1,190 MB | 0.69 / 0.94 | 0.95 / 0.90 |

These numbers use query vectors computed on the machine that built the index. Vectors computed on your machine (different decoder, CPU instead of GPU) will differ slightly; how much that costs in recall has not been measured yet.

## Installing

Run `BooruScope-Setup` (`BooruScope-Setup.exe` on Windows). It opens a small window where you choose:

- **Install location**: any folder you can write to. Default: `%LOCALAPPDATA%\Programs\BooruScope` on Windows, `~/Applications/BooruScope` on macOS, `~/.local/share/BooruScope` on Linux. Everything BooruScope stores, including the Python environment and the CLIP weights, stays in this folder.
- **Data**: the full index (pick M64, M96 or M128, and optionally the int8 rerank file) or the 1,000-post trial pack.
- **Shortcuts** (Windows): desktop and Start menu.

Downloads resume after an interruption and every file is checked against the SHA-256 in the published `manifest.json`. Running the installer again on the same folder updates the program, keeps verified data files, and only downloads what changed. The installer is the BooruScope program itself: any copy whose file name contains `setup`, or that is started with `-setup`, runs as the installer.

## Running

Start `booruscope` (`booruscope.exe`) in the install folder, or use the shortcut.

From source (Go 1.24+):

```
go run ./cmd/booruscope -home ./run
```

| Flag | Default | Meaning |
|---|---|---|
| `-home` | folder of the executable | settings, Python runtime, browser profile |
| `-data` | `<home>/data` | data files |
| `-port` | random | local port |
| `-no-window` | off | only print the local URL |
| `-browser` | auto | Chrome or Edge executable |
| `-python` | auto | Python used to create the venv |
| `-torch-index` | auto | pip index for PyTorch (`pypi` forces PyPI) |
| `-model` | `openai/clip-vit-large-patch14` | Hugging Face id or local folder |
| `-device` | `auto` | `cpu`, `cuda`, `cuda:1`, … |
| `-setup` | auto | run the installer |
| `-source` | `https://huggingface.co/Lanthanum4000/BooruScope/resolve/main/` | where the installer downloads data from |

Logs go to `<home>/runtime/booruscope.log` (installer: `booruscope-setup.log` in the temp folder).

## Using it

- Drop an image file from disk, or drag an image out of a browser window. BooruScope downloads linked images itself, sending the image's own origin as `Referer`.
- The query image stays pinned in the top-left 2×2 cells; results flow around it.
- **Per row** (3–6) resizes the grid. **Badge / Emoji** switches how the score is shown. **Index** picks M64/M96/M128. **int8 rerank** turns reranking on or off. **中文 / EN** switches the interface language.
- Double-click a card to open `https://gelbooru.com/index.php?page=post&s=view&id=<id>` in your default browser.

Defaults: 60 results per search, 32 × 60 = 1,920 rerank candidates, 6 concurrent thumbnail downloads, 256 MB thumbnail memory cache (never written to disk), scores shown as cosine similarity with three decimals.

## Search details

- Query `q` is the L2-normalized CLIP image embedding.
- Coarse score: `qr = q·R` (the mean is **not** subtracted), `table[s][c] = qr_s · centroid[s][c]`, `score = Σ_s table[s][code_s] + q·mean`.
- int8 rerank score: `Σ_j int8_j · scale_j · q_j` over the top `60 × 32` coarse candidates.

The preprocessing chain must match the one used to build the index:

1. Decode with Pillow, first frame only, no EXIF rotation, no ICC conversion. 16-bit greyscale keeps the high byte. Images with alpha are composited onto white.
2. Trim a uniform border: background = pixel (0,0); a pixel is content when `(19595·|ΔR| + 38470·|ΔG| + 7471·|ΔB| + 32768) >> 16 > 16`; keep the full frame if the box is the whole image, empty, or narrower than 8 px.
3. Resize the long edge to 448 (bicubic), then halve each axis (bilinear), then back to the 448 size (bilinear). Every resize is `torch.nn.functional.interpolate` with `align_corners=False, antialias=True`, rounded and clamped to uint8.
4. Resize the short edge to 224 (bicubic, same settings), centre-crop 224×224, scale to [0,1], normalize with the CLIP mean `(0.48145466, 0.4578275, 0.40821073)` and std `(0.26862954, 0.26130258, 0.27577711)`.
5. `CLIPVisionModelWithProjection` image embedding (fp16 on CUDA, fp32 otherwise), L2-normalized.

## Development

Publishing data files and building releases is described in [`docs/PUBLISHING.md`](docs/PUBLISHING.md).

```
go test ./...
python -m unittest discover -s internal/encoder/py -p "test_*.py"
```

The Go tests check the index readers, coarse search, int8 rerank and the HTTP API against `testdata/testpack/expected.json`.

## License

BooruScope's source code is released under the [MIT License](LICENSE). The CLIP model and the data files are distributed separately under their own terms.

## Model license

BooruScope does not ship model weights. On first start it downloads [`openai/clip-vit-large-patch14`](https://huggingface.co/openai/clip-vit-large-patch14) from Hugging Face into its own folder. The CLIP code is released under the MIT license; the model card states that deployed use cases are currently out of scope and that the model is intended for research. Read the [model card](https://huggingface.co/openai/clip-vit-large-patch14) before using it.

## Content

The index only covers posts rated general or sensitive, and skips posts tagged `nude`, `nipples`, `pussy`, `topless` or `underboob`. Thumbnails are loaded from Gelbooru when results are shown and are never stored on disk.
