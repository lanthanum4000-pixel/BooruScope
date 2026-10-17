import argparse
import base64
import io
import json
import sys

import numpy as np
from PIL import Image

TRIM_TOLERANCE = 16
TRIM_MIN_EDGE = 8
RELAY_LONG = 448
CLIP_EDGE = 224
CLIP_MEAN = (0.48145466, 0.4578275, 0.40821073)
CLIP_STD = (0.26862954, 0.26130258, 0.27577711)
MAX_PIXELS = 400_000_000


class InputError(Exception):
    pass


def to_8bit(im):
    if im.mode.startswith("I;16"):
        a = np.asarray(im, dtype=np.uint16) >> 8
        return Image.fromarray(a.astype(np.uint8))
    if im.mode in ("I", "F"):
        raise InputError("unsupported bit depth: " + im.mode)
    return im


def load_rgb(data):
    try:
        im = Image.open(io.BytesIO(data))
        im.seek(0)
        im.load()
    except Exception as e:
        raise InputError("cannot decode image: %s" % e) from e
    if im.width * im.height > MAX_PIXELS:
        raise InputError("image too large")
    alpha = im.mode in ("RGBA", "LA", "PA") or (im.mode == "P" and "transparency" in im.info)
    im = to_8bit(im)
    if not alpha:
        return np.asarray(im.convert("RGB"), dtype=np.uint8)
    rgba = im.convert("RGBA")
    out = Image.new("RGB", rgba.size, (255, 255, 255))
    out.paste(rgba, mask=rgba.getchannel("A"))
    return np.asarray(out, dtype=np.uint8)


def trim(t):
    import torch

    _, h, w = t.shape
    bg = t[:, 0, 0].to(torch.int32).view(3, 1, 1)
    d = (t.to(torch.int32) - bg).abs()
    luma = (d[0] * 19595 + d[1] * 38470 + d[2] * 7471 + 0x8000) >> 16
    mask = luma > TRIM_TOLERANCE
    rows = mask.any(1).nonzero().flatten().tolist()
    cols = mask.any(0).nonzero().flatten().tolist()
    if not rows:
        return t
    y0, y1, x0, x1 = rows[0], rows[-1] + 1, cols[0], cols[-1] + 1
    if (x0, y0, x1, y1) == (0, 0, w, h) or x1 - x0 < TRIM_MIN_EDGE or y1 - y0 < TRIM_MIN_EDGE:
        return t
    return t[:, y0:y1, x0:x1]


def resize(t, h, w, mode):
    import torch.nn.functional as F

    y = F.interpolate(t.unsqueeze(0).float(), size=(h, w), mode=mode, align_corners=False, antialias=True)
    return y.clamp(0, 255).round().to(dtype=t.dtype)[0]


def relay(t):
    t = trim(t)
    _, h, w = t.shape
    if w >= h:
        nw, nh = RELAY_LONG, max(1, round(h * RELAY_LONG / w))
    else:
        nh, nw = RELAY_LONG, max(1, round(w * RELAY_LONG / h))
    s = resize(t, nh, nw, "bicubic")
    d = resize(s, max(1, nh // 2), max(1, nw // 2), "bilinear")
    return resize(d, nh, nw, "bilinear")


def clip_input(t, mean, std):
    _, h, w = t.shape
    sc = CLIP_EDGE / min(w, h)
    nw, nh = max(CLIP_EDGE, round(w * sc)), max(CLIP_EDGE, round(h * sc))
    r = resize(t, nh, nw, "bicubic")
    top, left = (nh - CLIP_EDGE) // 2, (nw - CLIP_EDGE) // 2
    r = r[:, top:top + CLIP_EDGE, left:left + CLIP_EDGE]
    return (r.float() / 255.0 - mean) / std


def preprocess(data, device):
    import torch

    rgb = load_rgb(data)
    t = torch.from_numpy(np.array(rgb, dtype=np.uint8, copy=True)).permute(2, 0, 1).contiguous().to(device)
    mean = torch.tensor(CLIP_MEAN, device=device).view(3, 1, 1)
    std = torch.tensor(CLIP_STD, device=device).view(3, 1, 1)
    return clip_input(relay(t), mean, std)


class Encoder:
    def __init__(self, model_id, device):
        import torch
        from transformers import CLIPVisionModelWithProjection

        if device == "auto":
            device = "cuda" if torch.cuda.is_available() else "cpu"
        self.device = torch.device(device)
        self.half = self.device.type == "cuda" and torch.cuda.get_device_capability(self.device)[0] >= 7
        model = CLIPVisionModelWithProjection.from_pretrained(model_id)
        if self.half:
            model = model.half()
        self.model = model.to(self.device).eval()
        self.torch = torch

    def encode(self, data):
        torch = self.torch
        px = preprocess(data, self.device).unsqueeze(0)
        with torch.inference_mode():
            if self.half:
                px = px.half()
                with torch.autocast("cuda"):
                    v = self.model(pixel_values=px).image_embeds
            else:
                v = self.model(pixel_values=px).image_embeds
        v = v.float()[0]
        v = v / v.norm().clamp_min(1e-12)
        return v.cpu().numpy().astype("<f4")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--model", default="openai/clip-vit-large-patch14")
    ap.add_argument("--device", default="auto")
    args = ap.parse_args()
    out = sys.stdout
    sys.stdout = sys.stderr

    def emit(obj):
        out.write(json.dumps(obj) + "\n")
        out.flush()

    emit({"event": "loading"})
    try:
        enc = Encoder(args.model, args.device)
    except Exception as e:
        emit({"event": "fatal", "detail": "%s: %s" % (type(e).__name__, e)})
        return 1
    emit({"event": "ready", "device": str(enc.device), "half": enc.half})
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        rid = None
        try:
            req = json.loads(line)
            rid = req.get("id")
            vec = enc.encode(base64.b64decode(req["image"]))
            emit({"id": rid, "vector": base64.b64encode(vec.tobytes()).decode("ascii")})
        except InputError as e:
            emit({"id": rid, "error": "input", "detail": str(e)})
        except Exception as e:
            emit({"id": rid, "error": "internal", "detail": "%s: %s" % (type(e).__name__, e)})
    return 0


if __name__ == "__main__":
    sys.exit(main())
