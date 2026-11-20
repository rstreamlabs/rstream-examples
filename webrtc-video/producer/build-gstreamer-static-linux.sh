#!/usr/bin/env sh
set -eu

GST_VERSION="${GST_VERSION:-1.28.7}"
GST_ROOT="${GST_ROOT:-/opt/gstreamer}"
GST_FULL_LIBRARIES="${GST_FULL_LIBRARIES:-gstreamer-app-1.0,gstreamer-video-1.0}"
GST_FULL_ELEMENTS="${GST_FULL_ELEMENTS:-coreelements:capsfilter;videoconvertscale:videoconvert;videotestsrc:videotestsrc;videoparsersbad:h264parse;videoparsersbad:av1parse;x264:x264enc;aom:av1enc}"
GST_FULL_ARCHIVE="${GST_ROOT}/lib/libgstreamer-full-1.0.a"
X264_GIT_REF="${X264_GIT_REF:-b35605ace3ddf7c1a5d67a2eb553f034aef41d55}"
AOM_VERSION="${AOM_VERSION:-3.15.1}"
PKG_CONFIG_PATH_PREFIX="${GST_ROOT}/lib/pkgconfig:${GST_ROOT}/lib/gstreamer-1.0/pkgconfig"

fail() {
  echo "$*" >&2
  exit 1
}

# Overrides remain possible, but each source must have an immutable identity.
for version in "${GST_VERSION}" "${AOM_VERSION}"; do
  printf '%s\n' "${version}" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "invalid source version: ${version}"
done
case "${GST_VERSION}" in
  1.28.7) GST_SHA256="${GST_SHA256:-58dd845a6473355edd71f67227219ec04e039cee5c000265d3fbbd2e7920a3a8}" ;;
  *) : "${GST_SHA256:?provide GST_SHA256 when overriding GST_VERSION}" ;;
esac
case "${AOM_VERSION}" in
  3.15.1) AOM_SHA256="${AOM_SHA256:-8ca0c52746174603500f0adb6f2a215d69c9ca2aab2acb3caa06fb791d8d01bf}" ;;
  *) : "${AOM_SHA256:?provide AOM_SHA256 when overriding AOM_VERSION}" ;;
esac
for checksum in "${GST_SHA256}" "${AOM_SHA256}"; do
  case "${checksum}" in *[!0-9a-f]*|'') fail 'source SHA256 must contain 64 lowercase hexadecimal digits' ;; esac
  [ "${#checksum}" -eq 64 ] || fail 'source SHA256 must contain 64 lowercase hexadecimal digits'
done
case "${X264_GIT_REF}" in *[!0-9a-f]*|'') fail 'X264_GIT_REF must be a full immutable Git commit' ;; esac
[ "${#X264_GIT_REF}" -eq 40 ] || fail 'X264_GIT_REF must be a full immutable Git commit'

work_dir="$(mktemp -d /tmp/rstream-gstreamer-build.XXXXXX)"
trap 'rm -rf "${work_dir}"' 0
trap 'exit 130' INT
trap 'exit 143' HUP TERM
GST_SOURCE_DIR="${work_dir}/gstreamer-${GST_VERSION}"
GST_SOURCE_ARCHIVE="${work_dir}/gstreamer.tar.gz"
X264_SOURCE_DIR="${work_dir}/x264"
AOM_SOURCE_DIR="${work_dir}/libaom-${AOM_VERSION}"
AOM_SOURCE_ARCHIVE="${work_dir}/libaom.tar.gz"

download_verified() {
  curl --fail --location --silent --show-error --connect-timeout 15 --max-time 300 --retry 2 "$1" --output "$2"
  printf '%s  %s\n' "$3" "$2" | sha256sum -c -
}

# Verify both archives before executing any downloaded build scripts.
download_verified "https://storage.googleapis.com/aom-releases/libaom-${AOM_VERSION}.tar.gz" "${AOM_SOURCE_ARCHIVE}" "${AOM_SHA256}"
download_verified "https://gitlab.freedesktop.org/gstreamer/gstreamer/-/archive/${GST_VERSION}/gstreamer-${GST_VERSION}.tar.gz" "${GST_SOURCE_ARCHIVE}" "${GST_SHA256}"
tar -xzf "${AOM_SOURCE_ARCHIVE}" -C "${work_dir}"
tar -xzf "${GST_SOURCE_ARCHIVE}" -C "${work_dir}"

export PKG_CONFIG_PATH="${PKG_CONFIG_PATH_PREFIX}${PKG_CONFIG_PATH:+:${PKG_CONFIG_PATH}}"

git init "${X264_SOURCE_DIR}"
git -C "${X264_SOURCE_DIR}" remote add origin https://code.videolan.org/videolan/x264.git
git -C "${X264_SOURCE_DIR}" -c http.lowSpeedLimit=1024 -c http.lowSpeedTime=60 fetch --depth 1 origin "${X264_GIT_REF}"
git -C "${X264_SOURCE_DIR}" checkout --detach FETCH_HEAD
[ "$(git -C "${X264_SOURCE_DIR}" rev-parse HEAD)" = "${X264_GIT_REF}" ] || fail 'x264 commit verification failed'
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

cmake -S "${AOM_SOURCE_DIR}" -B "${work_dir}/aom-build" \
  -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_INSTALL_PREFIX="${GST_ROOT}" \
  -DBUILD_SHARED_LIBS=OFF \
  -DENABLE_DOCS=0 \
  -DENABLE_APPS=0 \
  -DENABLE_EXAMPLES=0 \
  -DENABLE_TESTDATA=0 \
  -DENABLE_TESTS=0 \
  -DENABLE_TOOLS=0

cmake --build "${work_dir}/aom-build" -j"$(getconf _NPROCESSORS_ONLN)"
cmake --install "${work_dir}/aom-build"

meson setup "${work_dir}/gst-build" "${GST_SOURCE_DIR}" \
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

ninja -C "${work_dir}/gst-build"
ninja -C "${work_dir}/gst-build" install

if [ ! -f "${GST_FULL_ARCHIVE}" ]; then
  echo "missing ${GST_FULL_ARCHIVE}" >&2
  exit 1
fi
