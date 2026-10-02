package service

import (
	"reflect"
	"testing"
)

// A dry run names what an upload would do without touching Telegram: only a
// replace upload reports the remote path it would overwrite.
func TestPlanUploadNamesReplaceTarget(t *testing.T) {
	got := PlanUpload([]string{"/tmp/a.txt", "/tmp/b.txt"}, "/docs", ConflictReplace)
	want := UploadPlan{Local: []string{"/tmp/a.txt", "/tmp/b.txt"}, Remote: "/docs", Policy: ConflictReplace, WouldReplace: "/docs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replace plan = %+v, want %+v", got, want)
	}
	for _, policy := range []ConflictPolicy{ConflictFail, ConflictSkip, ConflictRename} {
		if got := PlanUpload([]string{"/tmp/a.txt"}, "/a.txt", policy); got.WouldReplace != "" {
			t.Fatalf("%s plan would replace %q", policy, got.WouldReplace)
		}
	}
}
