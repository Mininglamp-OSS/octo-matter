// v3.3.3 §A fail-open regression net (caster R6 review).
//
// The fix is "remove the `if req.ActorUID != c.GetString("uid")` skip
// branch around HasDispatchedTaskForBotOnMatter". A future maintainer
// could reintroduce the same skip "for defensive future use" — which
// is exactly what shipped in faac159 and was caught at review. This
// regression net asserts the skip branch isn't reintroduced.
//
// Why source-grep instead of behavioral DB-mock: matter doesn't have a
// test harness like fleet doesn't; behavioral tests need real DB or
// a mocked dbr.SessionRunner, both heavyweight for what is structurally
// "is this 1 line of code present?". Same pragmatic choice as fleet's
// owner_regression_test.go.

package handler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteHandlers_NoActorSkipBranch_OnTaskBindingCheck(t *testing.T) {
	src := mustReadHandlerSource(t, "internal_handler.go")

	// The fail-open caster R6 caught had this exact shape:
	//   if req.ActorUID != c.GetString("uid") {
	//       hasTask, err := ... HasDispatchedTaskForBotOnMatter ...
	//   }
	// The fix removes the `if` wrapper — task binding always runs.
	// If a future change reintroduces any conditional skip around
	// the binding check, this test fires.
	forbiddenPatterns := []string{
		`if req.ActorUID != c.GetString("uid")`,
		`if req.ActorUID != c.GetString( "uid" )`, // tolerate spacing
		`if req.ActorUID == c.GetString("uid")`,   // either direction is the same fail-open shape
	}
	for _, p := range forbiddenPatterns {
		if strings.Contains(src, p) {
			t.Errorf(
				"internal_handler.go must NOT skip HasDispatchedTaskForBotOnMatter "+
					"based on actor_uid == ctx.uid (v3.3.3 §A fail-open caster R6 review). "+
					"Found forbidden pattern: %q.\n"+
					"If you have a legitimate reason to gate the task-binding check "+
					"differently for human-as-self writes, route them through the "+
					"user-facing /api/v1/matters/:id/timeline (CanAccessMatter-gated) "+
					"instead of weakening the /internal daemon writeback.",
				p)
		}
	}

	// Positive control: the binding check must still be invoked at all.
	if !strings.Contains(src, "HasDispatchedTaskForBotOnMatter") {
		t.Error("HasDispatchedTaskForBotOnMatter call missing from internal_handler.go — v3.3.3 §A regressed")
	}
	// Verify it's called from both writeback handlers (Timeline + Activity).
	if strings.Count(src, "HasDispatchedTaskForBotOnMatter") < 2 {
		t.Errorf("Expected HasDispatchedTaskForBotOnMatter invoked from BOTH WriteTimeline and WriteActivity (2 sites), got %d",
			strings.Count(src, "HasDispatchedTaskForBotOnMatter"))
	}
}

func mustReadHandlerSource(t *testing.T, filename string) string {
	t.Helper()
	dir, _ := os.Getwd()
	path := filepath.Join(dir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
