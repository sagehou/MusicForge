# Third-party notices

The React Button component is adapted from [shadcn/ui](https://github.com/shadcn-ui/ui), licensed under MIT:

MIT License

Copyright (c) 2023 shadcn

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

Runtime images include a pinned FFmpeg stable release built with Opus and LAME.
FFmpeg and LAME use LGPL licenses; Opus uses its upstream BSD license. The image
ships corresponding compressed source releases, FFmpeg's LGPL text, pinned source
checksums and the exact build script in `/usr/share/doc/musicforge/media`. Rebuild
with the `media-builder` stage in the repository Dockerfile to modify/relink the
media executables. FFmpeg is executed as a separate process. See
[FFmpeg legal information](https://ffmpeg.org/legal.html),
[Opus licensing](https://opus-codec.org/license/) and
[LAME](https://lame.sourceforge.io/).

Go and JavaScript dependencies retain their respective upstream licenses.
