package collectors

import (
	"strconv"
	"strings"
)

// separatorMode says what seriesName does with characters that do not belong
// in a series key segment.
type separatorMode int

const (
	// collapseSeparators turns every run of them into one underscore and drops
	// them at either end: "/media/pi/My Disk" → "media_pi_my_disk".
	collapseSeparators separatorMode = iota

	// keepSeparators turns each byte of them into its own underscore and keeps
	// them at either end: "br--lan." → "br__lan_". Only netinfo uses it, so
	// its wifi.<iface> keys stay what they were before the helpers were shared.
	keepSeparators
)

// seriesName turns free text (a sensor label, a mount point, an interface
// name) into one segment of a dotted series key: lowercase, with only a-z,
// 0-9 and _ left. mode decides how the other characters become underscores.
// The result can be empty; callers pick their own fallback.
func seriesName(s string, mode separatorMode) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	pending := false
	// Bytes, not runes: every byte of a multi-byte rune is outside a-z and
	// 0-9, so collapsing gives the same result either way, and keepSeparators
	// has always written one underscore per byte.
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			if pending && b.Len() > 0 {
				b.WriteByte('_')
			}
			pending = false
			b.WriteByte(c)
		case mode == keepSeparators:
			b.WriteByte('_')
		default:
			pending = true
		}
	}
	return b.String()
}

// uniqueName returns name, or name_2, name_3, ... if it is already in taken,
// and marks the result taken. Two NVMe drives become nvme and nvme_2;
// "/mnt/a_b" and "/mnt/a/b" both sanitise to mnt_a_b and become mnt_a_b and
// mnt_a_b_2.
func uniqueName(taken map[string]bool, name string) string {
	unique := name
	for i := 2; taken[unique]; i++ {
		unique = name + "_" + strconv.Itoa(i)
	}
	taken[unique] = true
	return unique
}
