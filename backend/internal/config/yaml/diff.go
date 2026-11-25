package yaml

import "strings"

// ComputeSimpleDiff is bounded in both output and memory. Above the LCS budget,
// render a coarse replacement rather than allocating quadratic memory.
func ComputeSimpleDiff(oldText, newText string) string {
	a, b := strings.TrimRight(oldText, "\n"), strings.TrimRight(newText, "\n")
	if a == b {
		return ""
	}
	old, new := strings.Split(a, "\n"), strings.Split(b, "\n")
	if a == "" {
		old = nil
	}
	if b == "" {
		new = nil
	}
	lines := []string{"--- active", "+++ proposed"}
	const maxLines = 4000
	add := func(prefix, value string) {
		if len(lines) < maxLines {
			lines = append(lines, prefix+value)
		}
	}
	if len(old) > 0 && len(new) > 1000000/len(old) {
		for _, v := range old {
			add("-", v)
		}
		for _, v := range new {
			add("+", v)
		}
		lines = append(lines, "... coarse diff; review candidate hash before apply ...")
		return strings.Join(lines, "\n")
	}
	dp := make([][]int, len(old)+1)
	for i := range dp {
		dp[i] = make([]int, len(new)+1)
	}
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(new) - 1; j >= 0; j-- {
			if old[i] == new[j] {
				dp[i][j] = 1 + dp[i+1][j+1]
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(old) && j < len(new) {
		if old[i] == new[j] {
			add(" ", old[i])
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			add("-", old[i])
			i++
		} else {
			add("+", new[j])
			j++
		}
	}
	for ; i < len(old); i++ {
		add("-", old[i])
	}
	for ; j < len(new); j++ {
		add("+", new[j])
	}
	if len(lines) >= maxLines {
		lines = append(lines, "... diff truncated ...")
	}
	return strings.Join(lines, "\n")
}

// ComputeChangesDiff omits unchanged lines and headers for a compact historical
// view. Common edges are removed first so large mostly-unchanged files remain
// cheap. A coarse replacement would falsely label unchanged lines as edits.
func ComputeChangesDiff(oldText, newText string) string {
	old := strings.Split(strings.TrimRight(oldText, "\n"), "\n")
	next := strings.Split(strings.TrimRight(newText, "\n"), "\n")
	for len(old) > 0 && len(next) > 0 && old[0] == next[0] {
		old, next = old[1:], next[1:]
	}
	for len(old) > 0 && len(next) > 0 && old[len(old)-1] == next[len(next)-1] {
		old, next = old[:len(old)-1], next[:len(next)-1]
	}
	diff := ComputeSimpleDiff(strings.Join(old, "\n"), strings.Join(next, "\n"))
	if strings.Contains(diff, "... coarse diff; review candidate hash before apply ...") {
		return "[changes unavailable: comparison exceeds budget]"
	}
	lines := strings.Split(diff, "\n")
	if len(lines) < 2 {
		return ""
	}
	changes := make([]string, 0, len(lines))
	for _, line := range lines[2:] {
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || line == "... diff truncated ..." {
			changes = append(changes, line)
		}
	}
	return strings.Join(changes, "\n")
}
