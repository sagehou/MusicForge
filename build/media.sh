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
fetch "flac-$FLAC_VERSION.tar.xz" "$FLAC_SHA256" "https://downloads.xiph.org/releases/flac/flac-$FLAC_VERSION.tar.xz"
cd "flac-$FLAC_VERSION"
./configure --disable-shared --enable-static --disable-ogg --disable-cpplibs --disable-doxygen-docs --disable-examples LDFLAGS=-static
make -j"$(nproc)"
make install
cp COPYING.Xiph COPYING.GPL /usr/local/share/musicforge/media/
cd /build
fetch "ffmpeg-$FFMPEG_VERSION.tar.xz" "$FFMPEG_SHA256" "https://ffmpeg.org/releases/ffmpeg-$FFMPEG_VERSION.tar.xz"
cd "ffmpeg-$FFMPEG_VERSION"
# Mixed audio input, Opus/MP3 output, JPEG/PNG artwork and CI fixtures.
./configure --disable-everything --disable-autodetect --disable-doc --disable-network \
    --enable-ffmpeg --enable-ffprobe --enable-static --disable-shared \
    --enable-libopus --enable-libmp3lame --pkg-config-flags=--static --extra-ldflags=-static \
    --enable-protocol=file,pipe --enable-indev=lavfi \
    --enable-demuxer=flac,ogg,mp3,mov,aac,wav,aiff,asf,ape,wv,matroska,image2,png_pipe,jpeg_pipe \
    --enable-muxer=flac,ogg,opus,mp3,ipod,mp4,adts,wav,aiff,asf,wv,matroska,image2,null \
    --enable-decoder=flac,opus,mp3,mp3float,aac,alac,vorbis,wmav1,wmav2,wmapro,wmalossless,ape,wavpack,mjpeg,png,pcm_s8,pcm_u8,pcm_s16le,pcm_s16be,pcm_s24le,pcm_s24be,pcm_s32le,pcm_s32be,pcm_s64le,pcm_s64be,pcm_f32le,pcm_f32be,pcm_f64le,pcm_f64be,pcm_alaw,pcm_mulaw \
    --enable-encoder=flac,libopus,libmp3lame,aac,alac,vorbis,wmav2,wavpack,mjpeg,pcm_s16le,pcm_s24le,pcm_s24be \
    --enable-parser=flac,opus,mpegaudio,aac,aac_latm,vorbis,mjpeg,png \
    --enable-filter=sine,anull,aformat,aresample,format,scale \
    --enable-swscale --enable-swresample --enable-avfilter --enable-zlib
make -j"$(nproc)"
make install
cp COPYING.LGPLv2.1 /usr/local/share/musicforge/media/
cp /src/build/media.sh /src/build/media-versions.env /usr/local/share/musicforge/media/
ffmpeg -version
ffprobe -version
flac --version
