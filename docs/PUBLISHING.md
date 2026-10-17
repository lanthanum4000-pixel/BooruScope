# Publishing data and releases

## Hugging Face layout

The installer reads everything from `https://huggingface.co/Lanthanum4000/BooruScope/resolve/main/`:

```
manifest.json
full/posts.bin
full/index_m64.pq
full/index_m96.pq
full/index_m128.pq
full/rerank_int8.bin
trial/posts.bin
trial/index_m64.pq
trial/index_m96.pq
trial/index_m128.pq
trial/rerank_int8.bin
```

`trial/` is the 1,000-post pack from [`testdata/testpack`](../testdata/testpack). A pack may leave out index files or the rerank file; the installer only offers what the manifest lists.

## Writing `manifest.json`

Put the files into a local folder with the layout above, then run:

```
go run ./cmd/bsmanifest <folder>
```

It checks that every file in a pack has the same number of rows as its `posts.bin`, hashes each file with SHA-256 and writes `<folder>/manifest.json`:

```json
{
  "format": "booruscope-manifest-v1",
  "packs": {
    "full": {
      "rows": 9181936,
      "files": [
        { "name": "posts.bin", "path": "full/posts.bin", "size": 183638740, "sha256": "…" }
      ]
    }
  }
}
```

Regenerate and upload the manifest whenever a data file changes; the installer refuses files whose hash does not match.

## Uploading

The repository must be public: the installer downloads without logging in.

```
pip install -U huggingface_hub
hf auth login
hf upload-large-folder Lanthanum4000/BooruScope <folder> --repo-type model --exclude manifest.json
hf upload Lanthanum4000/BooruScope <folder>/manifest.json manifest.json
```

`hf auth login` asks for an access token with write permission (Hugging Face → Settings → Access Tokens). `upload-large-folder` resumes if it is interrupted; run the same command again. The manifest goes last, so it never points at a file that is not there yet.

## Building a release

```
scripts/build-release.sh 0.1.0
```

This writes, for each platform, the program and a copy named `BooruScope-Setup` into `dist/`. The Windows build uses the GUI subsystem, so no console window opens.
