package media

import "bytes"

// h264ContainsIDR classifies an Annex B access unit already aligned by the
// parser. It does not validate slice contents or replace decoder conformance.
func h264ContainsIDR(data []byte) bool {
	for {
		start := bytes.Index(data, []byte{0, 0, 1})
		if start < 0 {
			return false
		}
		data = data[start+3:]
		end := bytes.Index(data, []byte{0, 0, 1})
		if end < 0 {
			end = len(data)
		}
		// Exclude forbidden headers, non-reference IDRs, and empty NALs.
		nal := bytes.TrimRight(data[:end], "\x00")
		if len(nal) > 1 && nal[0]&0x9f == 5 && nal[0]&0x60 != 0 {
			return true
		}
		if end == len(data) {
			return false
		}
		data = data[end:]
	}
}
