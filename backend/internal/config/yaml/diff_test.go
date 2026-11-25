package yaml

import (
	"strings"
	"testing"
)

func TestComputeSimpleDiffIdenticalInput(t *testing.T) {
	if got := ComputeSimpleDiff("a: 1\nb: 2\n", "a: 1\nb: 2\n"); got != "" {
		t.Errorf("identical input produced %q, want empty diff", got)
	}
}

func TestComputeSimpleDiffMidFileInsertion(t *testing.T) {
	got := ComputeSimpleDiff("line1\nline2\nline4", "line1\nline2\nline3\nline4")
	want := "--- active\n+++ proposed\n line1\n line2\n+line3\n line4"
	if got != want {
		t.Errorf("mid-file insertion:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestComputeSimpleDiffDeletion(t *testing.T) {
	got := ComputeSimpleDiff("line1\nline2\nline3", "line1\nline3")
	want := "--- active\n+++ proposed\n line1\n-line2\n line3"
	if got != want {
		t.Errorf("deletion:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestDiffAllocationBudgetForManyShortLines(t *testing.T) {
	old := strings.Repeat("x\n", 20000)
	next := strings.Repeat("y\n", 20000)
	diff := ComputeSimpleDiff(old, next)
	if len(diff) > 100000 || !strings.Contains(diff, "coarse diff") {
		t.Fatal("unbounded diff fallback")
	}
}

func TestChangesDiffOnlyActualEdits(t *testing.T) {
	for _, tc := range []struct{ old, next, want string }{
		{"a\nb\nc\n", "a\nx\nc\n", "-b\n+x"},
		{"same\n", "same\n", ""}, {"", "a\n", "+a"}, {"a\n", "", "-a"},
		{"a\nremove\nb\n", "a\nb\nadd\n", "-remove\n+add"},
	} {
		if got := ComputeChangesDiff(tc.old, tc.next); got != tc.want {
			t.Fatalf("unexpected edit lines: %q", got)
		}
	}
	common := strings.Repeat("unchanged\n", 5000)
	if got := ComputeChangesDiff(common+"before\n", common+"after\n"); got != "-before\n+after" {
		t.Fatal("large common prefix lost actual change")
	}
	if got := ComputeChangesDiff("first\n"+strings.Repeat("a\n", 1100)+"old\n", "new\n"+strings.Repeat("b\n", 1100)+"last\n"); !strings.HasPrefix(got, "[changes unavailable:") {
		t.Fatal("large comparison must not invent a coarse change")
	}
}
