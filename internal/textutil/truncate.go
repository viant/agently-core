package textutil

// RuneTruncate safely truncates a UTF-8 string to at most n runes without
// splitting a multi-byte character. When n <= 0 it returns an empty string.
func RuneTruncate(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	i := 0
	for idx := range s { // idx iterates over valid rune boundaries
		if i == n {
			return s[:idx]
		}
		i++
	}
	return s
}

// Head returns the first n bytes of s when it is longer than n.
// When n <= 0 it returns an empty string.
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// Tail returns the last n bytes of s when it is longer than n.
// When n <= 0 it returns an empty string.
func Tail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
