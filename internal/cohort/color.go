package cohort

import "fmt"

// Every cohort window gets a colour derived from its agent name, stored
// in window options that the default tmux.conf uses for the status bar
// and pane border, and tab-color.sh for the iTerm2 tab:
//
//	@cohort_color  "colourNNN"  (tmux style colour)
//	@cohort_rgb    "R G B"      (0-255 each)
//
// The mapping is the one the `team` tool used (POSIX cksum of the
// folder name into a fixed palette), so agents keep their colours.

// palette holds 256-colour indices, all light enough to carry black text.
var palette = []int{
	32, 33, 38, 39, 40, 41, 42, 43, 68, 69, 70, 71, 74, 75, 76, 77,
	80, 81, 105, 108, 110, 111, 113, 114, 132, 138, 140, 143, 146, 149, 150, 152,
	168, 173, 174, 175, 178, 179, 180, 181, 209, 210, 211, 214, 215, 216, 220, 222,
}

// colorOptions returns the window options carrying name's colour.
func colorOptions(name string) map[string]string {
	i := palette[cksum([]byte(name))%uint32(len(palette))]
	r, g, b := xtermRGB(i)
	return map[string]string{
		"@cohort_color": fmt.Sprintf("colour%d", i),
		"@cohort_rgb":   fmt.Sprintf("%d %d %d", r, g, b),
	}
}

// cksum is the POSIX cksum CRC: polynomial 0x04C11DB7, MSB first, over
// the data followed by its length (least significant byte first, no
// trailing zero bytes), complemented.
func cksum(data []byte) uint32 {
	var crc uint32
	add := func(b byte) {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	for _, b := range data {
		add(b)
	}
	for n := len(data); n > 0; n >>= 8 {
		add(byte(n))
	}
	return ^crc
}

// xtermRGB converts an xterm-256 colour index (16-255) to RGB.
func xtermRGB(i int) (r, g, b int) {
	if i >= 16 && i <= 231 {
		levels := []int{0, 95, 135, 175, 215, 255}
		i -= 16
		return levels[i/36], levels[(i%36)/6], levels[i%6]
	}
	v := 8 + 10*(i-232)
	return v, v, v
}
