import base64
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest

import numpy as np
from PIL import Image

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import encode

try:
    import torch
    import transformers
except ImportError:
    torch = None


def png_bytes(im):
    buf = io.BytesIO()
    im.save(buf, "PNG")
    return buf.getvalue()


class LoadTests(unittest.TestCase):
    def test_alpha_composites_on_white(self):
        im = Image.new("RGBA", (4, 4), (10, 20, 30, 0))
        im.putpixel((1, 1), (200, 100, 50, 255))
        rgb = encode.load_rgb(png_bytes(im))
        self.assertEqual(rgb.shape, (4, 4, 3))
        self.assertEqual(tuple(rgb[0, 0]), (255, 255, 255))
        self.assertEqual(tuple(rgb[1, 1]), (200, 100, 50))

    def test_palette_transparency(self):
        im = Image.new("P", (2, 1))
        im.putpalette([0, 0, 0, 255, 0, 0] + [0] * 762)
        im.putpixel((0, 0), 0)
        im.putpixel((1, 0), 1)
        im.info["transparency"] = 0
        rgb = encode.load_rgb(png_bytes(im))
        self.assertEqual(tuple(rgb[0, 0]), (255, 255, 255))
        self.assertEqual(tuple(rgb[0, 1]), (255, 0, 0))

    def test_16bit_grey_keeps_high_byte(self):
        a = np.full((2, 2), 0xAB12, dtype=np.uint16)
        im = Image.fromarray(a, "I;16")
        rgb = encode.load_rgb(png_bytes(im))
        self.assertEqual(int(rgb[0, 0, 0]), 0xAB)

    def test_first_frame_of_animation(self):
        frames = [Image.new("RGB", (3, 3), c) for c in ((255, 0, 0), (0, 0, 255))]
        buf = io.BytesIO()
        frames[0].save(buf, "GIF", save_all=True, append_images=frames[1:])
        rgb = encode.load_rgb(buf.getvalue())
        self.assertGreater(int(rgb[1, 1, 0]), 200)
        self.assertLess(int(rgb[1, 1, 2]), 50)

    def test_garbage_is_input_error(self):
        with self.assertRaises(encode.InputError):
            encode.load_rgb(b"not an image")


@unittest.skipIf(torch is None, "torch not installed")
class ChainTests(unittest.TestCase):
    def tensor(self, arr):
        return torch.from_numpy(arr).permute(2, 0, 1).contiguous()

    def test_trim_removes_uniform_border(self):
        a = np.full((40, 60, 3), 250, dtype=np.uint8)
        a[10:30, 5:45] = 0
        out = encode.trim(self.tensor(a))
        self.assertEqual(tuple(out.shape), (3, 20, 40))

    def test_trim_tolerance(self):
        a = np.full((40, 40, 3), 100, dtype=np.uint8)
        a[10:30, 10:30] = 116
        self.assertEqual(tuple(encode.trim(self.tensor(a)).shape), (3, 40, 40))
        a[10:30, 10:30] = 117
        self.assertEqual(tuple(encode.trim(self.tensor(a)).shape), (3, 20, 20))

    def test_trim_keeps_tiny_box(self):
        a = np.zeros((40, 40, 3), dtype=np.uint8)
        a[10:15, 10:30] = 255
        self.assertEqual(tuple(encode.trim(self.tensor(a)).shape), (3, 40, 40))

    def test_trim_blank_image(self):
        a = np.full((9, 9, 3), 7, dtype=np.uint8)
        self.assertEqual(tuple(encode.trim(self.tensor(a)).shape), (3, 9, 9))

    def test_relay_shape(self):
        rng = np.random.default_rng(0)
        a = rng.integers(0, 256, (300, 500, 3), dtype=np.uint8)
        r = encode.relay(self.tensor(a))
        self.assertEqual(tuple(r.shape), (3, 269, 448))
        self.assertEqual(r.dtype, torch.uint8)

    def test_clip_input_shape_and_range(self):
        rng = np.random.default_rng(1)
        a = rng.integers(0, 256, (448, 300, 3), dtype=np.uint8)
        mean = torch.tensor(encode.CLIP_MEAN).view(3, 1, 1)
        std = torch.tensor(encode.CLIP_STD).view(3, 1, 1)
        x = encode.clip_input(self.tensor(a), mean, std)
        self.assertEqual(tuple(x.shape), (3, 224, 224))
        self.assertLess(float(x.max()), 2.2)
        self.assertGreater(float(x.min()), -1.9)


@unittest.skipIf(torch is None, "torch not installed")
class ProtocolTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        from transformers import CLIPVisionConfig, CLIPVisionModelWithProjection

        torch.manual_seed(0)
        cfg = CLIPVisionConfig(hidden_size=32, intermediate_size=64, num_hidden_layers=2, num_attention_heads=2,
                               image_size=224, patch_size=32, projection_dim=768)
        cls.tmp = tempfile.TemporaryDirectory()
        CLIPVisionModelWithProjection(cfg).save_pretrained(cls.tmp.name)

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    def test_round_trip(self):
        script = os.path.join(os.path.dirname(os.path.abspath(__file__)), "encode.py")
        img = Image.new("RGB", (320, 200), (30, 120, 200))
        reqs = [
            {"id": 1, "image": base64.b64encode(png_bytes(img)).decode()},
            {"id": 2, "image": base64.b64encode(b"junk").decode()},
        ]
        stdin = "".join(json.dumps(r) + "\n" for r in reqs)
        p = subprocess.run([sys.executable, script, "--model", self.tmp.name, "--device", "cpu"],
                           input=stdin, capture_output=True, text=True, timeout=300)
        lines = [json.loads(x) for x in p.stdout.splitlines() if x.strip()]
        self.assertEqual(lines[0]["event"], "loading")
        self.assertEqual(lines[1]["event"], "ready")
        vec = np.frombuffer(base64.b64decode(lines[2]["vector"]), dtype="<f4")
        self.assertEqual(lines[2]["id"], 1)
        self.assertEqual(vec.shape, (768,))
        self.assertAlmostEqual(float(np.linalg.norm(vec)), 1.0, places=5)
        self.assertEqual(lines[3], {"id": 2, "error": "input", "detail": lines[3]["detail"]})


if __name__ == "__main__":
    unittest.main()
