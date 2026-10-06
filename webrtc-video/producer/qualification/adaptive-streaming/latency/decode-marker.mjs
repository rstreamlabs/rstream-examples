// A decoded marker is evidence of pixels, independent of RTP/RTCP rewriting.
// RGBA is the 256x32 crop at media-pixel coordinates (16, 16).
export function decodeMarker(rgba) {
  const width = 256,
    height = 32,
    cell = 8,
    columns = 32;
  if (
    !(rgba instanceof Uint8Array || rgba instanceof Uint8ClampedArray) ||
    rgba.length !== width * height * 4
  )
    throw new TypeError("Invalid marker crop");
  const bytes = new Uint8Array(16);
  let minimumContrast = 255;
  for (let bit = 0; bit < 128; bit++) {
    // Read the central 2x2 pixels, away from transform/quantization boundaries.
    const x = (bit % columns) * cell + 3;
    const y = Math.floor(bit / columns) * cell + 3;
    let sum = 0;
    for (let dy = 0; dy < 2; dy++)
      for (let dx = 0; dx < 2; dx++) {
        const offset = ((y + dy) * width + x + dx) * 4;
        sum += (rgba[offset] + rgba[offset + 1] + rgba[offset + 2]) / 3;
      }
    const intensity = sum / 4;
    // Ambiguous pixels fail instead of fabricating a plausible timestamp.
    if (intensity > 80 && intensity < 175) return null;
    minimumContrast = Math.min(
      minimumContrast,
      Math.abs(intensity - 127.5) * 2,
    );
    bytes[bit >> 3] |= (intensity >= 175 ? 1 : 0) << (7 - (bit % 8));
  }
  if (
    bytes[0] !== 0x52 ||
    bytes[1] !== 0x53 ||
    bytes[2] !== 2 ||
    bytes[3] !== 0
  )
    return null;
  let crc = 0xffffffff;
  for (const byte of bytes.subarray(0, 12)) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++)
      crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1));
  }
  const encodedCRC = new DataView(bytes.buffer).getUint32(12);
  if (~crc >>> 0 !== encodedCRC) return null;
  const microseconds = new DataView(bytes.buffer).getBigUint64(4);
  if (microseconds === 0n || microseconds > BigInt(Number.MAX_SAFE_INTEGER))
    return null;
  return {
    timestampMilliseconds: Number(microseconds) / 1000,
    minimumContrast,
  };
}
