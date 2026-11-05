package media

import "testing"

func TestH264ContainsIDR(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", nil, false},
		{"short prefix", []byte{0, 0, 1}, false},
		{"empty IDR", []byte{0, 0, 1, 0x65}, false},
		{"empty IDR before four byte prefix", []byte{0, 0, 1, 0x65, 0, 0, 0, 1, 0x41, 0x80}, false},
		{"forbidden bit", []byte{0, 0, 1, 0xe5, 0x80}, false},
		{"non-reference IDR", []byte{0, 0, 1, 5, 0x80}, false},
		{"three byte prefix", []byte{0, 0, 1, 0x65, 0x80}, true},
		{"four byte prefix", []byte{0, 0, 0, 1, 0x65, 0x80}, true},
		{"recovery SEI and non-IDR slice", []byte{0, 0, 1, 6, 6, 1, 0x80, 0, 0, 1, 0x41, 0x80}, false},
		{"SEI before IDR", []byte{0, 0, 1, 6, 1, 0x80, 0, 0, 0, 1, 0x65, 0x80}, true},
		{"escaped prefix", []byte{0, 0, 1, 0x41, 0, 0, 3, 1, 0x65, 0x80}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := h264ContainsIDR(tc.data); got != tc.want {
				t.Fatalf("got %t, want %t", got, tc.want)
			}
		})
	}
}
