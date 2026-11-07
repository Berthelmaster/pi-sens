package collectors

import "syscall"

// syscallStatter is the real fsStatter.
type syscallStatter struct{}

func (syscallStatter) Statfs(path string) (fsStats, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return fsStats{}, err
	}
	// df uses the fragment size and falls back to the block size when the
	// filesystem does not set one.
	size := uint64(st.Frsize)
	if size == 0 {
		size = uint64(st.Bsize)
	}
	return fsStats{
		BlockSize: size,
		Blocks:    st.Blocks,
		Bfree:     st.Bfree,
		Bavail:    st.Bavail,
		Files:     st.Files,
		Ffree:     st.Ffree,
	}, nil
}
