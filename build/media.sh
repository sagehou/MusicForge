#!/bin/sh
# Run only in GitHub Actions / Docker builders. Sources ship with the image.
set -eu
. /src/build/media-versions.env
mkdir -p /usr/local/share/musicforge/media /build
cd /build
fetch() {
    curl --fail --location --retry 3 "$3" -o "$1"
    printf '%s  %s\n' "$2" "$1" | sha256sum -c -
    cp "$1" /usr/local/share/musicforge/media/
    tar xf "$1"
}
fetch "opus-$OPUS_VERSION.tar.gz" "$OPUS_SHA256" "https://downloads.xiph.org/releases/opus/opus-$OPUS_VERSION.tar.gz"
cd "opus-$OPUS_VERSION"
./configure --disable-shared --enable-static --disable-extra-programs --disable-doc
make -j"$(nproc)"
make install
cd /build
fetch "lame-$LAME_VERSION.tar.gz" "$LAME_SHA256" "https://downloads.sourceforge.net/project/lame/lame/$LAME_VERSION/lame-$LAME_VERSION.tar.gz"
cd "lame-$LAME_VERSION"
./configure --disable-shared --enable-static --disable-frontend --disable-decoder
make -j"$(nproc)"
make install
cd /build
fetch "ffmpeg-$FFMPEG_VERSION.tar.xz" "$FFMPEG_SHA256" "https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz"
cd "ffmpeg-$FFMPEG_VERSION"
# FLAC input, Opus/MP3 output, JPEG/PNG artwork, full decode validation and CI fixtures.
./configure --disable-everything --disable-autodetect --disable-doc --disable-network \
    --enable-ffmpeg --enable-ffprobe --enable-static --disable-shared \
    --enable-libopus --enable-libmp3lame --pkg-config-flags=--static --extra-ldflags=-static \
    --enable-protocol=file,pipe --enable-indev=lavfi \
    --enable-demuxer=flac,ogg,mp3,image2,png_pipe,jpeg_pipe \
    --enable-muxer=flac,ogg,opus,mp3,image2,null \
    --enable-decoder=flac,opus,mp3,mp3float,mjpeg,png,pcm_s16le \
    --enable-encoder=flac,libopus,libmp3lame,mjpeg,pcm_s16le \
    --enable-parser=flac,opus,mpegaudio,mjpeg,png \
    --enable-filter=sine,anull,aformat,aresample,format,scale \
    --enable-swscale --enable-swresample --enable-avfilter --enable-zlib
make -j"$(nproc)"
make install
cp COPYING.LGPLv2.1 /usr/local/share/musicforge/media/
cp /src/build/media.sh /src/build/media-versions.env /usr/local/share/musicforge/media/
ffmpeg -version
ffprobe -version
