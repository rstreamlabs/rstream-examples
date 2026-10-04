/* Qualification-only raw-frame timestamp. Never part of the production build. */
#include <gst/gst.h>
#include <gst/video/gstvideofilter.h>
#include <stdint.h>
#include <string.h>

#define PACKAGE "rstream-video-qualification"
#define MARKER_X 16
#define MARKER_Y 16
#define CELL_SIZE 8
#define COLUMNS 32
#define ROWS 4
#define MARKER_BYTES 16

typedef struct { GstVideoFilter parent; } GstLatencyStamp;
typedef struct { GstVideoFilterClass parent; } GstLatencyStampClass;

G_DEFINE_TYPE(GstLatencyStamp, gst_latency_stamp, GST_TYPE_VIDEO_FILTER)

static const char *caps =
    "video/x-raw,format=I420,width=(int)[288,2147483647],"
    "height=(int)[64,2147483647],framerate=(fraction)[0/1,2147483647/1]";

static guint32 marker_crc(const guint8 *bytes, gsize length) {
  guint32 crc = 0xffffffffu;
  for (gsize i = 0; i < length; i++) {
    crc ^= bytes[i];
    for (guint bit = 0; bit < 8; bit++)
      crc = (crc >> 1) ^ (0xedb88320u & (0u - (crc & 1u)));
  }
  return ~crc;
}

static GstFlowReturn stamp_frame(GstVideoFilter *filter, GstVideoFrame *frame) {
  (void)filter;
  /* Version 2 uses the Linux monotonic clock, not adjustable wall time. */
  guint8 bytes[MARKER_BYTES] = {0x52, 0x53, 2, 0};
  const gint64 now = g_get_monotonic_time();
  if (now <= 0) return GST_FLOW_ERROR;
  guint64 timestamp = (guint64)now;
  for (gint i = 11; i >= 4; i--) {
    bytes[i] = (guint8)(timestamp & 0xffu);
    timestamp >>= 8;
  }
  guint32 crc = marker_crc(bytes, 12);
  for (gint i = 15; i >= 12; i--) {
    bytes[i] = (guint8)(crc & 0xffu);
    crc >>= 8;
  }
  guint8 *luma = GST_VIDEO_FRAME_PLANE_DATA(frame, 0);
  const gint stride = GST_VIDEO_FRAME_PLANE_STRIDE(frame, 0);
  for (guint bit = 0; bit < MARKER_BYTES * 8; bit++) {
    const guint8 value = (bytes[bit / 8] & (1u << (7 - bit % 8))) ? 235 : 16;
    const guint x = MARKER_X + (bit % COLUMNS) * CELL_SIZE;
    const guint y = MARKER_Y + (bit / COLUMNS) * CELL_SIZE;
    for (guint row = 0; row < CELL_SIZE; row++)
      memset(luma + (gssize)(y + row) * stride + x, value, CELL_SIZE);
  }
  for (guint plane = 1; plane < 3; plane++) {
    guint8 *chroma = GST_VIDEO_FRAME_PLANE_DATA(frame, plane);
    const gint chroma_stride = GST_VIDEO_FRAME_PLANE_STRIDE(frame, plane);
    for (guint row = MARKER_Y / 2; row < (MARKER_Y + ROWS * CELL_SIZE) / 2; row++)
      memset(chroma + (gssize)row * chroma_stride + MARKER_X / 2, 128,
             COLUMNS * CELL_SIZE / 2);
  }
  return GST_FLOW_OK;
}

static void gst_latency_stamp_init(GstLatencyStamp *self) {
  /* This diagnostic must not introduce its own QoS/drop policy. */
  gst_base_transform_set_qos_enabled(GST_BASE_TRANSFORM(self), FALSE);
  gst_base_transform_set_in_place(GST_BASE_TRANSFORM(self), TRUE);
}

static void gst_latency_stamp_class_init(GstLatencyStampClass *klass) {
  GstElementClass *element = GST_ELEMENT_CLASS(klass);
  GstVideoFilterClass *video = GST_VIDEO_FILTER_CLASS(klass);
  GstCaps *supported = gst_caps_from_string(caps);
  gst_element_class_add_pad_template(element,
      gst_pad_template_new("sink", GST_PAD_SINK, GST_PAD_ALWAYS, supported));
  gst_element_class_add_pad_template(element,
      gst_pad_template_new("src", GST_PAD_SRC, GST_PAD_ALWAYS, supported));
  gst_caps_unref(supported);
  gst_element_class_set_static_metadata(element,
      "Raw-frame latency stamp", "Filter/Video",
      "Qualification timestamp before encoding; requires a shared host clock",
      "rstream");
  video->transform_frame_ip = stamp_frame;
}

static gboolean plugin_init(GstPlugin *plugin) {
  return gst_element_register(plugin, "rstreamlatencystamp", GST_RANK_NONE,
                              gst_latency_stamp_get_type());
}

GST_PLUGIN_DEFINE(GST_VERSION_MAJOR, GST_VERSION_MINOR, latencystamp,
    "Qualification-only raw-frame timestamp", plugin_init, "0.0.2", "MIT/X11",
    PACKAGE, "https://github.com/rstreamlabs/rstream-examples")
