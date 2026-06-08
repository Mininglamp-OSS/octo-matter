// v3.3.3 §A fail-open regression net (caster R6 review) + v3.3.4 §B2/§B3
// regression nets (lml2468 R7).
//
// R6 (v3.3.3): "remove the `if req.ActorUID != c.GetString("uid")` skip
// branch around HasDispatchedTaskForBotOnMatter". A future maintainer
// could reintroduce the same skip "for defensive future use" — which
// is exactly what shipped in faac159 and was caught at review. This
// regression net asserts the skip branch isn't reintroduced.
//
// R8 (v3.3.4 §B2 lml2468 R7 P0): the same handler had the shape
//   if err != nil || !hasTask { failCode(403, NO_TASK_BINDING) }
// which collapsed transient DB errors into a permanent 403 — daemon
// contract treats NO_TASK_BINDING as "give up forever", so any DB blip
// permanently lost a legit task lease. The fix splits err vs !hasTask
// into two branches. This regression net asserts the joint `err != nil
// ||` form on the same line as HasDispatched isn't reintroduced.
//
// R7 (v3.3.4 §B3 lml2468 R7 P0): RecordAgentActivity now returns error;
// any sync call site that drops it (bare call, `_ = ...`) defeats the
// 5xx propagation. Async fire-and-forget call sites are allowed but
// must use `if err := ...; err != nil { log... }` with a literal
// `// allowed-async-fire-and-forget` marker.
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
	"regexp"
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

// TestWriteHandlers_NoErrOrHasTaskCollapse — v3.3.4 §B2 lml2468 R7 P0
// regression net (R8 lesson, "error 状态空间"). Pre-v3.3.4 the writeback
// handlers had `if err != nil || !hasTask { failCode(403, NO_TASK_BINDING) }`
// which collapsed transient DB error into a permanent 403, breaking
// §H3's 5xx→503 daemon retry contract. The fix splits the two branches.
// If a future change collapses them back, this test fires.
func TestWriteHandlers_NoErrOrHasTaskCollapse(t *testing.T) {
	src := mustReadHandlerSource(t, "internal_handler.go")

	// Forbid: `if err != nil || ` AND `!hasTask` on the same line (the
	// collapsed pattern). Tolerate `if err != nil` standalone — that's
	// the fixed shape.
	forbiddenLine := regexp.MustCompile(`if\s+err\s*!=\s*nil\s*\|\|\s*!hasTask`)
	for ln, line := range strings.Split(src, "\n") {
		if forbiddenLine.MatchString(line) {
			t.Errorf(
				"internal_handler.go:%d collapses transient DB error into 403 NO_TASK_BINDING "+
					"(v3.3.4 §B2 lml2468 R7 P0 R8 lesson). Line: %q\n"+
					"Split into:\n"+
					"  if err != nil { respondErr(c, err); return }   // → 500, daemon retries\n"+
					"  if !hasTask { failCode(403, NO_TASK_BINDING) } // permanent give-up",
				ln+1, line)
		}
	}
}

// TestRecordAgentActivity_NoBareCall_OnNewSyncSites — v3.3.4 §B3 lml2468 R7
// P0 regression net. RecordAgentActivity returns error since v3.3.4; any
// bare call (no `if err :=`, no `err :=`, no `_ = ...`) silently swallows
// the 5xx propagation. Async fire-and-forget call sites are allowed but
// must use `if err := ...; err != nil { log... }` shape with a literal
// `// allowed-async-fire-and-forget` marker on the same line or the
// immediately previous line.
//
// syncFiles whitelist scans all files that contain RecordAgentActivity
// calls — including timeline_handler.go even though its calls are inside
// worker.Submit(func() { ... }) closures (same as matter_handler.go's
// dispatchOneBotAssignee). Both rely on the literal
// `// allowed-async-fire-and-forget` marker to be recognized as the
// fire-and-forget async path. Scope-consistent: any future call in
// either file gets the same enforcement (v3.3.4 review bot P3 — keep
// the rule symmetric across the two call-bearing files).
func TestRecordAgentActivity_NoBareCall_OnNewSyncSites(t *testing.T) {
	syncFiles := []string{"matter_handler.go", "internal_handler.go", "timeline_handler.go"}
	// bareCallRE matches: `<receiver>.RecordAgentActivity(...)` with the
	// open-paren on the same line, NOT preceded by `if err :=`, `err :=`,
	// `:= ...,` (multi-assign), or `_ =`. We anchor to the receiver dot
	// to skip comments/docs that mention the function name in prose.
	bareCallRE := regexp.MustCompile(`^\s*[A-Za-z_][\w]*\.RecordAgentActivity\s*\(`)
	allowedAsyncMarker := "// allowed-async-fire-and-forget"
	for _, fn := range syncFiles {
		src := mustReadHandlerSource(t, fn)
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !bareCallRE.MatchString(line) {
				continue
			}
			// Check this line AND the previous line for the async-marker
			// (gives caller flexibility to put the marker on the line
			// above the `if err :=` block).
			prev := ""
			if i > 0 {
				prev = lines[i-1]
			}
			if strings.Contains(line, allowedAsyncMarker) || strings.Contains(prev, allowedAsyncMarker) {
				continue
			}
			t.Errorf("%s:%d: bare RecordAgentActivity call without err handling — wrap in "+
				"`if err := ...; err != nil { respondErr(c, err); return }` (sync) or "+
				"`if err := ...; err != nil { log.Printf(...) } // allowed-async-fire-and-forget` "+
				"(worker.Submit async). v3.3.4 §B3 lml2468 R7 P0 lesson. Line: %q",
				fn, i+1, line)
		}
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
