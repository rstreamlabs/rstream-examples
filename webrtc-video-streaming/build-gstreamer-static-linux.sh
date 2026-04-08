#!/usr/bin/env sh
set -eu

GST_VERSION="${GST_VERSION:-1.28.1}"
GST_ROOT="${GST_ROOT:-/opt/gstreamer}"
GST_FULL_LIBRARIES="${GST_FULL_LIBRARIES:-gstreamer-app-1.0,gstreamer-video-1.0}"
GST_FULL_ELEMENTS="${GST_FULL_ELEMENTS:-coreelements:capsfilter;videoconvertscale:videoconvert;videotestsrc:videotestsrc;videoparsersbad:h264parse;videoparsersbad:av1parse;x264:x264enc;aom:av1enc}"
GST_FULL_ARCHIVE="${GST_ROOT}/lib/libgstreamer-full-1.0.a"
GST_SOURCE_DIR="/tmp/gstreamer-${GST_VERSION}"
GST_SOURCE_ARCHIVE="/tmp/gstreamer-${GST_VERSION}.tar.gz"
X264_GIT_REF="${X264_GIT_REF:-stable}"
X264_SOURCE_DIR="/tmp/x264"
AOM_VERSION="${AOM_VERSION:-3.13.1}"
AOM_SOURCE_DIR="/tmp/libaom-${AOM_VERSION}"
AOM_SOURCE_ARCHIVE="/tmp/libaom-${AOM_VERSION}.tar.gz"
PKG_CONFIG_PATH_PREFIX="${GST_ROOT}/lib/pkgconfig:${GST_ROOT}/lib/gstreamer-1.0/pkgconfig"

export PKG_CONFIG_PATH="${PKG_CONFIG_PATH_PREFIX}${PKG_CONFIG_PATH:+:${PKG_CONFIG_PATH}}"

rm -rf "${X264_SOURCE_DIR}"
git clone --depth 1 --branch "${X264_GIT_REF}" https://code.videolan.org/videolan/x264.git "${X264_SOURCE_DIR}"
(cd "${X264_SOURCE_DIR}" &&
  ./configure \
    --prefix="${GST_ROOT}" \
    --enable-static \
    --disable-cli \
    --host="$(cc -dumpmachine)" \
    --bit-depth=8 \
    --chroma-format=420 &&
  make -j"$(getconf _NPROCESSORS_ONLN)" &&
  make install)

rm -rf "${AOM_SOURCE_DIR}" /tmp/libaom/build
curl -fsSL "https://storage.googleapis.com/aom-releases/libaom-${AOM_VERSION}.tar.gz" -o "${AOM_SOURCE_ARCHIVE}"
tar -xzf "${AOM_SOURCE_ARCHIVE}" -C /tmp

cmake -S "${AOM_SOURCE_DIR}" -B /tmp/libaom/build \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="${GST_ROOT}" \
  -DBUILD_SHARED_LIBS=OFF \
  -DENABLE_DOCS=0 \
  -DENABLE_EXAMPLES=0 \
  -DENABLE_TESTDATA=0 \
  -DENABLE_TESTS=0 \
  -DENABLE_TOOLS=0

cmake --build /tmp/libaom/build -j"$(getconf _NPROCESSORS_ONLN)"
cmake --install /tmp/libaom/build

rm -rf "${GST_SOURCE_DIR}" /tmp/gstreamer/build
curl -fsSL "https://gitlab.freedesktop.org/gstreamer/gstreamer/-/archive/${GST_VERSION}/gstreamer-${GST_VERSION}.tar.gz" -o "${GST_SOURCE_ARCHIVE}"
tar -xzf "${GST_SOURCE_ARCHIVE}" -C /tmp

meson setup /tmp/gstreamer/build "${GST_SOURCE_DIR}" \
  --buildtype=release \
  --prefix="${GST_ROOT}" \
  --default-library=static \
  -Dauto_features=disabled \
  -Dgst-full=enabled \
  -Dbase=enabled \
  -Dbad=enabled \
  -Dugly=enabled \
  -Dgood=disabled \
  -Dlibav=disabled \
  -Dpython=disabled \
  -Dintrospection=disabled \
  -Dglib:tests=false \
  -Dglib:installed_tests=false \
  -Dglib:introspection=disabled \
  -Dglib:nls=disabled \
  -Dglib:glib_debug=disabled \
  -Dglib:glib_assert=false \
  -Dglib:glib_checks=false \
  -Ddevtools=disabled \
  -Dexamples=disabled \
  -Dtests=disabled \
  -Ddoc=disabled \
  -Dgpl=enabled \
  -Dgst-plugins-base:app=enabled \
  -Dgst-plugins-base:videoconvertscale=enabled \
  -Dgst-plugins-base:videotestsrc=enabled \
  -Dgst-plugins-bad:aom=enabled \
  -Dgst-plugins-bad:videoparsers=enabled \
  -Dgst-plugins-ugly:x264=enabled \
  -Dgstreamer:benchmarks=disabled \
  -Dgstreamer:introspection=disabled \
  -Dgstreamer:tests=disabled \
  -Dgstreamer:tools=disabled \
  -Dgst-full-target-type=static_library \
  -Dgst-full-libraries="${GST_FULL_LIBRARIES}" \
  -Dgst-full-elements="${GST_FULL_ELEMENTS}"

ninja -C /tmp/gstreamer/build
ninja -C /tmp/gstreamer/build install

if [ ! -f "${GST_FULL_ARCHIVE}" ]; then
  echo "missing ${GST_FULL_ARCHIVE}" >&2
  exit 1
fi
