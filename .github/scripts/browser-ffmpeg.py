#!/usr/bin/env python3
"""Actions browser fixture: make selected encodes observable and cancellable."""
import os
from pathlib import Path
import sys

args = sys.argv[1:]
slow = Path(os.environ["MUSICFORGE_CONFIG_DIR"]).parent / "slow-encode"
if slow.exists() and any(codec in args for codec in ("libopus", "libmp3lame")):
    args.insert(args.index("-i"), "-re")
os.execvp("ffmpeg", ["ffmpeg", *args])
